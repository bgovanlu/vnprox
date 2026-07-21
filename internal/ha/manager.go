package ha

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Default lease tunables (docs/deployment.md documents the operator-facing
// [ha] equivalents). Chosen so a real failover completes in a few seconds
// without flapping on a single slow/blipped replication pass: the active
// renews every RenewInterval; a standby promotes only after the last-observed
// lease has expired past FencingMargin; an isolated active that cannot confirm
// its lease with the peer for a whole LeaseTTL self-demotes (fail-safe).
const (
	DefaultLeaseTTL      = 15 * time.Second
	DefaultRenewInterval = 5 * time.Second
	DefaultFencingMargin = 15 * time.Second
	// DefaultReplicationLagThreshold is the audit-id gap (rows the standby has
	// not yet applied) beyond which GET /ha/status reports degraded and the
	// ha_replication_degraded finding fires.
	DefaultReplicationLagThreshold = 500
)

// Config configures a Manager. Lease and Coordinator are required. Replicator/
// Source/Applier are the replication seam: a single-daemon (non-HA) deployment
// leaves them nil, in which case the Manager still owns the lease locally
// (always active, never self-demotes) but replicates nothing.
type Config struct {
	Clock         Clock
	Logger        *slog.Logger
	Lease         LeaseStore
	Replicator    Replicator
	Source        SnapshotSource
	Applier       Applier
	Coordinator   Coordinator
	Announcer     Announcer
	InstanceID    string
	LeaseTTL      time.Duration
	RenewInterval time.Duration
	FencingMargin time.Duration
	LagThreshold  int64
	// Bootstrap, when true, makes this daemon acquire term 1 as the active at
	// Start if no lease has ever been recorded/observed — exactly one daemon in
	// a fresh pair is configured to bootstrap, so a first-boot pair never both
	// claim term 1.
	Bootstrap bool
}

// Status is GET /ha/status's response shape.
type Status struct {
	Role                string `json:"role"`
	LastError           string `json:"lastError,omitempty"`
	Term                int64  `json:"term"`
	LeaseExpiresAt      int64  `json:"leaseExpiresAt"`
	ReplicationLag      int64  `json:"replicationLag"`
	ReplicationDegraded bool   `json:"replicationDegraded"`
}

// Manager owns this daemon's HA role, lease, and replication. See the package
// doc comment for the single-writer / split-brain guarantee.
type Manager struct {
	observedAt  time.Time
	lastPushOK  time.Time
	applier     Applier
	leaseStore  LeaseStore
	repl        Replicator
	source      SnapshotSource
	coord       Coordinator
	announcer   Announcer
	lastErr     error
	clock       Clock
	log         *slog.Logger
	instanceID  string
	role        Role
	lease       Lease
	observed    Lease
	leaseTTL    time.Duration
	renew       time.Duration
	margin      time.Duration
	lag         int64
	auditCursor int64
	lagThresh   int64
	mu          sync.Mutex
	bootstrap   bool
	degraded    bool
}

// NewManager constructs a Manager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Lease == nil {
		return nil, fmt.Errorf("ha: Config.Lease is required")
	}
	if cfg.Coordinator == nil {
		return nil, fmt.Errorf("ha: Config.Coordinator is required")
	}
	if cfg.InstanceID == "" {
		return nil, fmt.Errorf("ha: Config.InstanceID is required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = clockFunc(time.Now)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	leaseTTL := cfg.LeaseTTL
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}
	renew := cfg.RenewInterval
	if renew <= 0 {
		renew = DefaultRenewInterval
	}
	margin := cfg.FencingMargin
	if margin <= 0 {
		margin = DefaultFencingMargin
	}
	lagThresh := cfg.LagThreshold
	if lagThresh <= 0 {
		lagThresh = DefaultReplicationLagThreshold
	}
	return &Manager{
		clock: clock, log: logger, leaseStore: cfg.Lease, repl: cfg.Replicator,
		source: cfg.Source, applier: cfg.Applier, coord: cfg.Coordinator, announcer: cfg.Announcer,
		instanceID: cfg.InstanceID, role: RoleStandby,
		leaseTTL: leaseTTL, renew: renew, margin: margin, lagThresh: lagThresh, bootstrap: cfg.Bootstrap,
	}, nil
}

// Start establishes this daemon's initial role from the persisted lease. A
// still-valid lease this daemon itself holds is resumed as active (re-arming
// timers from the same persisted deadlines); anything else starts standby.
// Bootstrap acquires term 1 as active when no lease has ever existed.
func (m *Manager) Start(ctx context.Context) error {
	now := m.clock.Now()
	persisted, err := m.leaseStore.Get(ctx)
	switch {
	case err == nil:
		m.mu.Lock()
		m.lease = persisted
		if persisted.Holder == m.instanceID && now.Unix() <= persisted.ExpiresAt {
			m.role = RoleActive
			m.mu.Unlock()
			m.log.Info("ha: resuming active from valid persisted lease", "term", persisted.Term)
			return m.becomeActive(ctx, false)
		}
		// A lease we don't hold (or an expired one): start standby, treating
		// the persisted record as the last-known observation so we honour its
		// expiry+margin before considering promotion.
		m.observed = persisted
		m.observedAt = now
		m.mu.Unlock()
		m.log.Info("ha: starting standby", "observed_term", persisted.Term, "observed_holder", persisted.Holder)
		return nil
	case errors.Is(err, ErrNoLease):
		if m.bootstrap {
			return m.promoteFrom(ctx, 0)
		}
		m.mu.Lock()
		// No lease anywhere yet: give the (bootstrapping) peer one lease TTL of
		// grace to make contact before this standby would consider promoting.
		m.observed = Lease{ExpiresAt: now.Add(m.leaseTTL).Unix()}
		m.observedAt = now
		m.mu.Unlock()
		m.log.Info("ha: starting standby (no lease yet, awaiting peer)")
		return nil
	default:
		return fmt.Errorf("ha: reading persisted lease at start: %w", err)
	}
}

// RunLoop drives Tick on a real-time RenewInterval ticker until ctx is
// cancelled — cmd/vnproxd's runGroup actor signature (mirrors
// change.Service.RunScheduler). Tests drive Tick directly against an injected
// Clock instead.
func (m *Manager) RunLoop(ctx context.Context) error {
	ticker := time.NewTicker(m.renew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.Tick(ctx)
		}
	}
}

// Tick performs one arbitration step: the active renews its lease and pushes a
// replication batch; a standby checks whether the observed lease has expired
// past the fencing margin and, if so, promotes.
func (m *Manager) Tick(ctx context.Context) {
	m.mu.Lock()
	role := m.role
	m.mu.Unlock()
	if role == RoleActive {
		m.tickActive(ctx)
		return
	}
	m.tickStandby(ctx)
}

// tickActive renews the lease locally and (if a replicator is wired) pushes the
// batch. A push carrying a superseding term demotes this daemon; a push that
// keeps failing for a whole LeaseTTL self-demotes an isolated active (fail-safe
// — it can no longer confirm it is the sole writer).
func (m *Manager) tickActive(ctx context.Context) {
	now := m.clock.Now()
	m.mu.Lock()
	m.lease.ExpiresAt = now.Add(m.leaseTTL).Unix()
	m.lease.UpdatedAt = now.Unix()
	lease := m.lease
	since := m.auditCursor
	m.mu.Unlock()

	if err := m.leaseStore.Set(ctx, lease); err != nil {
		m.log.Error("ha: persisting renewed lease", "error", err)
	}

	if m.repl == nil || m.source == nil {
		return // single-daemon: nothing to replicate, never self-demotes
	}

	batch, err := m.source.Gather(ctx, since)
	if err != nil {
		m.log.Error("ha: gathering replication batch", "error", err)
		return
	}
	batch.Lease = lease

	ack, pushErr := m.repl.Push(ctx, batch)
	if pushErr != nil {
		m.handlePushFailure(ctx, now, pushErr)
		return
	}
	m.handlePushAck(ctx, now, ack)
}

// handlePushFailure records a failed replication pass and self-demotes an
// active that has been unable to confirm its lease with the peer for a whole
// LeaseTTL (fail-safe against split-brain: an isolated active steps down rather
// than keep driving where a standby may be about to promote).
func (m *Manager) handlePushFailure(ctx context.Context, now time.Time, pushErr error) {
	m.mu.Lock()
	m.lastErr = pushErr
	m.degraded = true
	stale := !m.lastPushOK.IsZero() && now.Sub(m.lastPushOK) >= m.leaseTTL
	m.mu.Unlock()
	m.log.Warn("ha: replication push failed", "error", pushErr, "self_demote", stale)
	if stale {
		m.demote(ctx, 0, "isolated active could not confirm lease for a full TTL")
	}
}

// handlePushAck records a successful pass, advances the audit cursor, updates
// replication lag, and demotes if the peer reports a superseding term.
func (m *Manager) handlePushAck(ctx context.Context, now time.Time, ack Ack) {
	hw, hwErr := m.source.AuditHighWater(ctx)
	m.mu.Lock()
	m.lastErr = nil
	m.lastPushOK = now
	m.auditCursor = ack.AuditMaxID
	if hwErr == nil {
		m.lag = hw - ack.AuditMaxID
		if m.lag < 0 {
			m.lag = 0
		}
		m.degraded = m.lag > m.lagThresh
	}
	superseded := ack.Term > m.lease.Term
	newerTerm := ack.Term
	m.mu.Unlock()

	if superseded {
		m.demote(ctx, newerTerm, "peer reported a newer lease term")
	}
}

// tickStandby promotes if the observed lease has expired past the fencing
// margin (a genuine failover), and never otherwise.
func (m *Manager) tickStandby(ctx context.Context) {
	now := m.clock.Now()
	m.mu.Lock()
	promote := now.Unix() > m.observed.ExpiresAt+int64(m.margin.Seconds())
	m.mu.Unlock()
	if !promote {
		return
	}
	// The observed lease expired past the fencing margin: a real failover.
	// Promote to a strictly-higher term than anything observed.
	m.mu.Lock()
	base := m.lease.Term
	if m.observed.Term > base {
		base = m.observed.Term
	}
	m.mu.Unlock()
	if err := m.promoteFrom(ctx, base); err != nil {
		m.log.Error("ha: promotion failed", "error", err)
	}
}

// promoteFrom acquires the lease at base+1 and becomes active, re-arming timers
// from the persisted (replicated) absolute deadlines through the change
// engine's existing re-arm path.
func (m *Manager) promoteFrom(ctx context.Context, base int64) error {
	now := m.clock.Now()
	m.mu.Lock()
	m.lease = Lease{
		Holder: m.instanceID, Term: base + 1,
		ExpiresAt: now.Add(m.leaseTTL).Unix(), AcquiredAt: now.Unix(), UpdatedAt: now.Unix(),
	}
	m.role = RoleActive
	term := m.lease.Term
	m.mu.Unlock()
	m.log.Info("ha: promoting to active", "term", term)
	return m.becomeActive(ctx, true)
}

// becomeActive persists the lease, re-arms the change engine (reproducing the
// same absolute deadlines — never fresh ones), and triggers the failover
// announce. rearm is false only on a resume where Start already knows the timer
// state is intact (it re-arms regardless — ArmPendingRollbacks is idempotent).
func (m *Manager) becomeActive(ctx context.Context, _ bool) error {
	m.mu.Lock()
	lease := m.lease
	m.lastPushOK = m.clock.Now() // grace: don't immediately self-demote on the first tick
	m.mu.Unlock()

	if err := m.leaseStore.Set(ctx, lease); err != nil {
		return fmt.Errorf("ha: persisting acquired lease: %w", err)
	}
	if err := m.coord.ReArm(ctx); err != nil {
		// A re-arm failure is loud but not fatal to leadership: the node-local
		// timers (T-304) still hold each node's absolute deadline independently.
		m.log.Error("ha: re-arming apply/confirm timers on promotion", "error", err)
	}
	if m.announcer != nil {
		if err := m.announcer.Announce(ctx, RoleActive); err != nil {
			m.log.Error("ha: announcing active (VIP/DNS failover)", "error", err)
		}
	}
	return nil
}

// demote steps down to standby: quiesce the change engine (cancel in-process
// timers without touching persisted status) so this daemon drives nothing, and
// adopt newerTerm if it is higher than what we hold. Idempotent.
func (m *Manager) demote(ctx context.Context, newerTerm int64, reason string) {
	m.mu.Lock()
	if m.role != RoleActive {
		m.mu.Unlock()
		return
	}
	m.role = RoleStandby
	if newerTerm > m.lease.Term {
		m.lease.Term = newerTerm
		m.lease.Holder = ""
	}
	m.mu.Unlock()

	m.log.Warn("ha: demoting to standby", "reason", reason, "newer_term", newerTerm)
	m.coord.Quiesce()
	if m.announcer != nil {
		if err := m.announcer.Announce(ctx, RoleStandby); err != nil {
			m.log.Error("ha: announcing standby on demotion", "error", err)
		}
	}
}

// Receive applies an incoming replication push (the standby side). It enforces
// the fence: a batch whose sender term is older than ours is rejected (not
// applied) and answered with our higher term so the stale sender demotes; a
// batch at our term or newer is applied, its lease recorded as the live
// observation, and — if newer — adopted (demoting us if we were somehow active
// on an older term).
func (m *Manager) Receive(ctx context.Context, batch Batch) (Ack, error) {
	now := m.clock.Now()
	m.mu.Lock()
	myTerm := m.lease.Term
	role := m.role
	m.mu.Unlock()

	senderTerm := batch.Lease.Term
	stale := senderTerm < myTerm || (role == RoleActive && senderTerm == myTerm && batch.Lease.Holder != m.instanceID)
	if stale {
		// We are the current-or-newer leader; do not apply the stale sender's
		// data. Report our term so it demotes.
		return m.ack(ctx, role), nil
	}

	if m.applier != nil {
		if err := m.applier.Apply(ctx, batch); err != nil {
			return Ack{}, fmt.Errorf("ha: applying replicated batch: %w", err)
		}
	}

	m.mu.Lock()
	m.observed = batch.Lease
	m.observedAt = now
	wasActiveOnOlderTerm := m.role == RoleActive && senderTerm > m.lease.Term
	if senderTerm > m.lease.Term {
		m.lease = batch.Lease
		m.role = RoleStandby
	}
	newRole := m.role
	m.mu.Unlock()

	if wasActiveOnOlderTerm {
		m.log.Warn("ha: demoting — received a newer lease term over replication", "term", senderTerm)
		m.coord.Quiesce()
		if m.announcer != nil {
			if err := m.announcer.Announce(ctx, RoleStandby); err != nil {
				m.log.Error("ha: announcing standby on replication-driven demotion", "error", err)
			}
		}
	}
	return m.ack(ctx, newRole), nil
}

// ack builds this daemon's reply to a push.
func (m *Manager) ack(ctx context.Context, role Role) Ack {
	var hw int64
	if m.applier != nil {
		if v, err := m.applier.AuditHighWater(ctx); err == nil {
			hw = v
		}
	}
	m.mu.Lock()
	term := m.lease.Term
	m.mu.Unlock()
	return Ack{Term: term, Role: string(role), AuditMaxID: hw}
}

// IsLeader reports whether this daemon currently holds the leader lease — the
// LeaderGuard change.Service consults before any unattended, timer-driven
// apply/confirm/rollback decision (T-1704's single-writer fence). A demoted or
// never-promoted daemon returns false, so its callbacks fire as no-ops.
func (m *Manager) IsLeader() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.role == RoleActive
}

// Role returns the current role.
func (m *Manager) Role() Role {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.role
}

// Status returns the current GET /ha/status view.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Role: string(m.role), Term: m.lease.Term, LeaseExpiresAt: m.lease.ExpiresAt,
		ReplicationLag: m.lag, ReplicationDegraded: m.degraded,
	}
	if m.lastErr != nil {
		st.LastError = m.lastErr.Error()
	}
	return st
}

// ReplicationDegraded reports whether replication is currently degraded (the
// last push failed, or the standby is lagging past the configured threshold) —
// the ha_replication_degraded finding's input.
func (m *Manager) ReplicationDegraded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.degraded
}
