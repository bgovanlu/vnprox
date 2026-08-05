package change

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// topicChangesets is the WS subscription topic name clients use for
// changeset.status events (docs/api.md's WebSocket section:
// `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks"]}`).
const topicChangesets = "changesets"

// Broadcaster is the seam this package uses to fan out changeset.status WS
// events without depending on internal/topology's Hub type directly —
// mirrors the seam pattern internal/api's AuthService/TopologyService/
// LayoutStore interfaces already use for this codebase's cross-package
// dependencies. topology.Service.Broadcast (see that package's hub.go)
// satisfies this: docs/api.md documents a single shared /api/ws connection
// multiplexing "topology", "changesets", "metrics:<ref>", and "tasks"
// topics alike, so this package reuses T-106's hub rather than standing up
// a second WebSocket endpoint.
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// InventorySource is the seam Service uses to obtain a read snapshot of
// live network state for validation (T-202): *inventory.Graph satisfies
// this via its existing Snapshot method, so wiring in cmd/vnproxd just
// passes the same *inventory.Graph instance topology/collect already share
// — this package never polls or mutates inventory itself.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// AllocationsSource is the seam Service uses to fetch live IPAM allocation
// data for T-406's DHCP-range-overlap advisory check
// (validate_advisory.go's checkDHCPRangeOverlap, SafetyOptions.Allocations'
// doc comment): cmd/vnproxd wires a small adapter around *ipam.Service
// (internal/change deliberately never imports internal/ipam directly —
// the same "small seam, adapted by the caller" convention every other
// cross-package Config dependency here already follows). Optional: nil
// disables the check (dhcpAllocations then always returns nil, exactly
// like a nil Inventory validates against an empty snapshot).
type AllocationsSource interface {
	DHCPRangeAllocations(ctx context.Context) ([]DHCPRangeAllocation, error)
}

// SecretSealer seals a plaintext op-embedded secret at rest with AES-256-GCM
// (internal/store.SessionCipher — the same primitive sessions.pve_ticket_enc
// and the wireguard_* sealed columns use). It is the seam Service uses to seal
// a wg.peer.add op's preshared key into WgPeerAddParams.PresharedKeyEnc at
// stage/create time, so the plaintext PSK never lands in changesets.ops_json
// or on a changeset read response (docs/security.md's WireGuard
// credential-storage note). A nil Sealer means this deployment stages no
// secret-bearing op; if one arrives anyway, Create/UpdateDraft fail closed
// rather than persist it in the clear.
type SecretSealer interface {
	Encrypt(plaintext []byte) ([]byte, error)
}

// ClusterMembershipSource is T-1201's optional seam for resolving which
// attached cluster each PVE node belongs to, so the cross-cluster changeset
// scoping check (validate_crosscluster.go) can reject an op targeting a node
// in a different cluster than the changeset's ClusterID. In a federated
// deployment cmd/vnproxd wires internal/federation.Aggregator here (it
// already fans out every cluster's node list); a single-cluster deployment
// leaves it nil, which — together with the implicit-default-cluster ("")
// convention — makes the check a complete no-op, so nothing changes for the
// non-federated case. Soft-fail by contract: a read error yields no
// membership rather than blocking validation on a transient hiccup, the same
// stance AllocationsSource above already takes.
type ClusterMembershipSource interface {
	NodeClusters(ctx context.Context) (map[string]string, error)
}

// MetricsRecorder is T-1903's self-observability seam onto the change
// engine: *metrics.Registry satisfies it. Nil (the default) disables
// recording entirely — the same nil-safe-optional-dependency convention
// every other Config seam in this file already follows.
//
// Scoping note (see apply.go's ChangeOp* constants and their call sites):
// this only fires for a call that passed its state-machine precondition
// check and attempted a real mutation — a rejection for "wrong status" /
// "not found" / "locked" / "validation blocked" is visible via the HTTP
// layer's own RED status-class metric and the audit trail already, not
// duplicated here.
type MetricsRecorder interface {
	// ObserveChangeOutcome records one change-engine operation's terminal
	// outcome, op one of the ChangeOp* constants.
	ObserveChangeOutcome(op string, success bool)
	// ObserveAwaitingConfirmDuration records how long a changeset spent in
	// awaiting_confirm before leaving it, outcome one of "committed",
	// "rolled_back", "failed".
	ObserveAwaitingConfirmDuration(outcome string, dur time.Duration)
}

// Config configures a Service. Changesets and Audit are required; WS and
// Inventory are optional (nil disables WS broadcasting / validates against
// an empty snapshot, respectively — e.g. in tests that don't need them) and
// Now/Logger default sensibly when zero, mirroring internal/auth.Config's
// same conventions.
type Config struct {
	Clock  Clock
	Timers NodeTimerAgent
	Qos    QosGateway
	WG     WGGateway
	Sealer SecretSealer
	// RevertGateways (T-1805 / D1) rebuilds a PVEGateway from the ticket
	// sealed at apply time, so the unattended commit-confirm-timeout and
	// crash-recovery reverts can restore a changeset's `fw.*`/`sdn.*` portion
	// with no live user session. nil disables sealing entirely: nothing is
	// captured, and the apply response reports unattended revert as
	// unavailable — the pre-T-1805 behaviour, stated rather than implied.
	RevertGateways    RevertGatewayFactory
	WgCarriers        WgCarrierSource
	Switches          SwitchGateway
	SwitchScope       SwitchScopeSource
	Nodes             NodeAgent
	Refresher         InventoryRefresher
	WS                Broadcaster
	Inventory         InventorySource
	Allocations       AllocationsSource
	ClusterMembership ClusterMembershipSource
	ImpactPreflight   ImpactPreflighter
	Snapshots         *store.SnapshotRepo
	Schedules         *store.ChangeScheduleRepo
	Logger            *slog.Logger
	Now               func() time.Time
	// Metrics (T-1903) is the self-observability recorder for apply/confirm/
	// rollback/unattended-revert outcomes and awaiting_confirm duration.
	// Nil disables recording.
	Metrics MetricsRecorder
	// LeaderGuard, when set, is consulted immediately before any UNATTENDED,
	// timer-driven apply/confirm/rollback decision this daemon would make on
	// its own (the commit-confirm auto-rollback timer and the scheduler's
	// fire tick) — T-1704's single-writer fencing hook. It returns true only
	// while this daemon currently holds the HA leader lease; a false answer
	// makes the callback a logged no-op, so a demoted or fenced former-active
	// never drives a rollback/apply the current lease-term holder is (or will
	// be) responsible for. nil (the default, and every non-HA deployment)
	// means "always this daemon" — behaviour is identical to pre-T-1704.
	// Interactive, human-initiated Apply/Confirm/Rollback API calls are NOT
	// gated here: those flow through the API's own auth/role checks and the
	// single active daemon is the only one serving the API behind the VIP.
	LeaderGuard        func() bool
	Blobs              *store.BlobRepo
	Changesets         *store.ChangesetRepo
	TimerFunc          TimerFunc
	Audit              *store.AuditRepo
	ProtectedPath      string
	CorosyncPath       string
	LocalClusterID     string
	RollbackWindowDays int
	ConfirmTimeout     time.Duration
	SwitchPushEnabled  bool
	AllowDangerousOps  bool
}

// TimerFunc arms a one-shot timer that runs f after d and can be stopped.
// *time.Timer satisfies Stopper, so time.AfterFunc is the production default.
type TimerFunc func(d time.Duration, f func()) Stopper

// Stopper is the subset of *time.Timer the rollback timer needs (Stop).
type Stopper interface {
	Stop() bool
}

// Service implements T-201's changeset draft CRUD (store-backed
// persistence on top of T-003's *store.ChangesetRepo, the status state
// machine in changeset.go, WS `changeset.status` broadcasts on every
// status transition, and audit entries on create/discard) plus T-202's
// validation: Findings are (re)computed on every draft mutation
// (docs/features/change-management.md §2: "Runs on every draft change")
// via Validate.go's pipeline, and the exported Validate method backs
// `POST /changesets/{id}/validate` and additionally promotes/demotes the
// draft<->validated status transition. Diff/Apply/Confirm/Rollback are
// T-205's responsibility — see doc.go.
type Service struct {
	ws                 Broadcaster
	nodes              NodeAgent
	qos                QosGateway
	wg                 WGGateway
	sealer             SecretSealer
	revertGateways     RevertGatewayFactory
	wgCarriers         WgCarrierSource
	switches           SwitchGateway
	allocations        AllocationsSource
	inv                InventorySource
	refresher          InventoryRefresher
	nodeTimers         NodeTimerAgent
	clock              Clock
	switchScope        SwitchScopeSource
	membership         ClusterMembershipSource
	impactPreflight    ImpactPreflighter
	schedules          *store.ChangeScheduleRepo
	timers             map[string]Stopper
	repo               *store.ChangesetRepo
	snapshots          *store.SnapshotRepo
	blobs              *store.BlobRepo
	audit              *store.AuditRepo
	log                *slog.Logger
	newTimer           TimerFunc
	now                func() time.Time
	metrics            MetricsRecorder
	leaderGuard        func() bool
	lockHeldBy         string
	corosyncPath       string
	protectedPath      string
	localClusterID     string
	scheduleSecret     []byte
	confirmTimeout     time.Duration
	rollbackWindowDays int
	applyMu            sync.Mutex
	switchPushEnabled  bool
	allowDangerousOps  bool
}

// Commit-confirm window bounds (docs/features/change-management.md §4).
const (
	DefaultConfirmTimeout = 120 * time.Second
	MinConfirmTimeout     = 30 * time.Second
	MaxConfirmTimeout     = 600 * time.Second
)

// DefaultRollbackWindowDays is the documented manual-rollback window for a
// committed changeset (docs/features/change-management.md §4: "offered for
// 7 days"), matching store.DefaultSnapshotPinDays so the window and the
// snapshot-retention pin agree by default.
const DefaultRollbackWindowDays = 7

// NewService constructs a Service. Config.Changesets and Config.Audit are
// required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Changesets == nil {
		return nil, fmt.Errorf("change: Config.Changesets is required")
	}
	if cfg.Audit == nil {
		return nil, fmt.Errorf("change: Config.Audit is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	protectedPath := cfg.ProtectedPath
	if protectedPath == "" {
		protectedPath = DefaultProtectedPath
	}
	confirmTimeout := cfg.ConfirmTimeout
	if confirmTimeout == 0 {
		confirmTimeout = DefaultConfirmTimeout
	}
	timerFunc := cfg.TimerFunc
	if timerFunc == nil {
		timerFunc = func(d time.Duration, f func()) Stopper { return time.AfterFunc(d, f) }
	}
	rollbackWindowDays := cfg.RollbackWindowDays
	if rollbackWindowDays <= 0 {
		rollbackWindowDays = DefaultRollbackWindowDays
	}
	clock := cfg.Clock
	if clock == nil {
		clock = clockFunc(now)
	}
	return &Service{
		repo: cfg.Changesets, audit: cfg.Audit, ws: cfg.WS, inv: cfg.Inventory, allocations: cfg.Allocations, now: now, log: logger,
		protectedPath: protectedPath, corosyncPath: cfg.CorosyncPath, allowDangerousOps: cfg.AllowDangerousOps,
		localClusterID: cfg.LocalClusterID, membership: cfg.ClusterMembership, impactPreflight: cfg.ImpactPreflight,
		nodes: cfg.Nodes, nodeTimers: cfg.Timers, qos: cfg.Qos, wg: cfg.WG, sealer: cfg.Sealer, revertGateways: cfg.RevertGateways, wgCarriers: cfg.WgCarriers, snapshots: cfg.Snapshots, blobs: cfg.Blobs, refresher: cfg.Refresher,
		switches: cfg.Switches, switchScope: cfg.SwitchScope, switchPushEnabled: cfg.SwitchPushEnabled,
		confirmTimeout:     clampConfirmTimeout(confirmTimeout),
		rollbackWindowDays: rollbackWindowDays,
		timers:             map[string]Stopper{},
		newTimer:           timerFunc,
		schedules:          cfg.Schedules,
		clock:              clock,
		leaderGuard:        cfg.LeaderGuard,
		scheduleSecret:     newScheduleSecret(logger),
		metrics:            cfg.Metrics,
	}, nil
}

// clampConfirmTimeout bounds d to [MinConfirmTimeout, MaxConfirmTimeout].
func clampConfirmTimeout(d time.Duration) time.Duration {
	if d < MinConfirmTimeout {
		return MinConfirmTimeout
	}
	if d > MaxConfirmTimeout {
		return MaxConfirmTimeout
	}
	return d
}

// mayLead reports whether this daemon is currently permitted to make an
// unattended, timer-driven apply/confirm/rollback decision (T-1704's
// single-writer fence). A nil LeaderGuard — every non-HA deployment — always
// permits, so behaviour is identical to pre-T-1704. When a guard is wired, it
// returns true only while this daemon holds the HA leader lease; the fail-safe
// stance is that an ambiguous/false answer withholds the action (a withheld
// auto-rollback simply leaves the changeset awaiting_confirm for the true
// leader's own re-armed timer to resolve — never a double-drive).
func (s *Service) mayLead() bool {
	return s.leaderGuard == nil || s.leaderGuard()
}

// applyConfigured reports whether the apply-engine dependencies are wired.
func (s *Service) applyConfigured() bool {
	return s.nodes != nil && s.snapshots != nil && s.blobs != nil
}

// nullString is a small helper for the apply engine's store writes.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// inventorySnapshot returns the current inventory snapshot to validate
// against, or an empty one if no InventorySource was configured (tests and
// any caller that doesn't need real referential checks).
func (s *Service) inventorySnapshot() inventory.Snapshot {
	if s.inv == nil {
		return inventory.NewGraph().Snapshot()
	}
	return s.inv.Snapshot()
}

// safetyOptions builds T-203's SafetyOptions for one validation call: the
// protected-interface set is loaded fresh from s.protectedPath every time
// (docs/features/blueprints.md §3's onboarding-correction flow needs the
// very next validation to see a just-saved correction, not a cached
// snapshot from daemon startup), and allow_dangerous_ops is the value
// captured at Service construction from config.Config.Safety.
// AllowDangerousOps. A load failure degrades to "nothing protected" rather
// than failing validation outright, logged at error level so an operator
// notices a broken protected.json without every validate call 500ing.
func (s *Service) safetyOptions() SafetyOptions {
	cfg, err := LoadProtectedConfig(s.protectedPath)
	if err != nil {
		s.log.Error("change: loading protected-interface config for validation", "path", s.protectedPath, "error", err)
		return SafetyOptions{AllowDangerousOps: s.allowDangerousOps}
	}
	protected, bad := cfg.Resolve()
	for _, ref := range bad {
		s.log.Warn("change: protected-interface config has an unparsable ref, ignoring", "ref", ref)
	}
	return SafetyOptions{Protected: protected, AllowDangerousOps: s.allowDangerousOps}
}

// dhcpAllocations fetches live IPAM allocation data for T-406's
// DHCP-range-overlap advisory check (SafetyOptions.Allocations), or nil
// when no AllocationsSource is configured or the live read fails — a
// soft-fail read exactly like every other optional enrichment source in
// this codebase (e.g. internal/ipam's own agentObservations), never
// blocking validation on a transient PVE hiccup.
func (s *Service) dhcpAllocations(ctx context.Context) []DHCPRangeAllocation {
	if s.allocations == nil {
		return nil
	}
	allocs, err := s.allocations.DHCPRangeAllocations(ctx)
	if err != nil {
		s.log.Debug("change: reading live IPAM allocations for DHCP-range overlap check failed, skipping", "error", err)
		return nil
	}
	return allocs
}

// auditSafetyOverride records an audit entry when allow_dangerous_ops
// caused one or more of T-203's safety-interlock findings to be downgraded
// from error to warning (docs/security.md: "override only via config flag
// allow_dangerous_ops"; T-203's card: "its use audited"). It is a no-op
// unless the override actually mattered for this validation (i.e. a
// safety-class finding is present at all — it would have been an error,
// and short-circuited the changeset, had the override not been active).
func (s *Service) auditSafetyOverride(ctx context.Context, author, changesetID string, findings []Finding) {
	if !s.allowDangerousOps {
		return
	}
	var refs []string
	for _, f := range findings {
		if f.Code == codeProtectedInterface || f.Code == codeGuestBearingBridge {
			refs = append(refs, f.Ref)
		}
	}
	if len(refs) == 0 {
		return
	}
	s.appendAudit(ctx, author, "changeset.safety_override", "warning", changesetID, map[string]any{"refs": refs})
}

// List returns changesets ordered newest-first, optionally filtered to a
// single status (an empty string lists all).
func (s *Service) List(ctx context.Context, status string) ([]Changeset, error) {
	rows, err := s.repo.List(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("change: listing changesets: %w", err)
	}
	out := make([]Changeset, 0, len(rows))
	for _, row := range rows {
		c, err := fromStoreRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Get returns the changeset with the given id. The returned error wraps
// store.ErrNotFound (checkable with errors.Is) if no such changeset
// exists.
func (s *Service) Get(ctx context.Context, id string) (Changeset, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return Changeset{}, fmt.Errorf("change: getting changeset %s: %w", id, err)
	}
	c, err := fromStoreRow(row)
	if err != nil {
		return Changeset{}, err
	}
	// T-1805: recompute the unattended-revert coverage report so a reload of
	// GET /changesets/{id} shows the same promise the apply response made,
	// rather than losing it with the response body. Pure computation over
	// {ops, confirm deadline, sealed-ticket expiry} — no credential is read.
	c.UnattendedRevert = s.unattendedRevertFor(c)
	return c, nil
}

// Create persists a new draft changeset authored by author, audits the
// creation, and broadcasts its initial `changeset.status` (draft) event.
// A nil ops is stored as an empty list, never a JSON null. Findings are
// computed immediately against the current inventory snapshot
// (docs/features/change-management.md §2: validation "runs on every draft
// change"), though the changeset's Status stays StatusDraft regardless of
// what they contain — only the explicit Validate call promotes a clean
// draft to StatusValidated.
func (s *Service) Create(ctx context.Context, author, title string, ops []Op) (Changeset, error) {
	return s.create(ctx, author, title, ops, OriginUI, "")
}

// CreateWithOrigin is Create with an explicit provenance label (T-1701): the
// MCP surface (internal/mcp) calls it with OriginMCP and the staging token's
// api_tokens.id so every AI-staged draft is unerasably labelled in the audit
// trail. It is otherwise byte-identical to Create — the same validation, the
// same StatusDraft result, the same single-in-flight-apply guarantees downstream
// — because an MCP-staged changeset is an ordinary changeset that a human still
// applies through the change engine; origin is a label, never a different code
// path. An empty origin normalizes to OriginUI so a mis-wired caller can never
// produce an unlabelled row.
func (s *Service) CreateWithOrigin(ctx context.Context, author, title string, ops []Op, origin, originTokenID string) (Changeset, error) {
	return s.create(ctx, author, title, ops, origin, originTokenID)
}

func (s *Service) create(ctx context.Context, author, title string, ops []Op, origin, originTokenID string) (Changeset, error) {
	if ops == nil {
		ops = []Op{}
	}
	if origin == "" {
		origin = OriginUI
	}
	if err := s.sealOpSecrets(ops); err != nil {
		return Changeset{}, err
	}
	nowUnix := s.now().Unix()
	findings := s.validateScoped(ctx, s.localClusterID, ops)
	c := Changeset{
		ID: store.NewULID(), Title: title, Author: author, Status: StatusDraft, ClusterID: s.localClusterID,
		Origin: origin, OriginTokenID: originTokenID,
		Ops: ops, Findings: findings, CreatedAt: nowUnix, UpdatedAt: nowUnix,
	}
	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Insert(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: creating changeset %s: %w", c.ID, err)
	}
	s.appendAudit(ctx, author, "changeset.create", "success", c.ID, map[string]any{"title": title, "opCount": len(ops), "origin": origin})
	s.auditSafetyOverride(ctx, author, c.ID, findings)
	s.broadcastStatus(c)
	return c, nil
}

// CreateRequest creates a T-1703 request-changeset: identical to Create (ops
// are sealed and validated exactly like a draft's, findings attached) except
// the changeset enters StatusRequested rather than StatusDraft, so it is
// blocked from apply until an approver calls Approve. The tenant linkage
// (which tenant owns the request, who raised it) is recorded by the caller in
// the changeset_requests table — this package deliberately holds no tenant
// knowledge; it only owns the changeset lifecycle. The audit row carries
// origin "request" so a reviewer can always tell a tenant-requested changeset
// from an ordinary draft.
func (s *Service) CreateRequest(ctx context.Context, author, title string, ops []Op) (Changeset, error) {
	if ops == nil {
		ops = []Op{}
	}
	if err := s.sealOpSecrets(ops); err != nil {
		return Changeset{}, err
	}
	nowUnix := s.now().Unix()
	findings := s.validateScoped(ctx, s.localClusterID, ops)
	c := Changeset{
		ID: store.NewULID(), Title: title, Author: author, Status: StatusRequested, ClusterID: s.localClusterID,
		Ops: ops, Findings: findings, CreatedAt: nowUnix, UpdatedAt: nowUnix,
	}
	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Insert(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: creating request-changeset %s: %w", c.ID, err)
	}
	s.appendAudit(ctx, author, "changeset.request", "success", c.ID, map[string]any{"title": title, "opCount": len(ops)})
	s.auditSafetyOverride(ctx, author, c.ID, findings)
	s.broadcastStatus(c)
	return c, nil
}

// Approve converts a StatusRequested changeset into an ordinary StatusDraft
// (T-1703): the request-changeset gate. It re-validates the ops against the
// current inventory snapshot (the state may have moved since the request was
// raised) and returns *ErrIllegalTransition if the changeset is not currently
// in StatusRequested. Approval is NOT apply — the approver still drives the
// ordinary draft -> validate -> apply -> confirm flow afterward. The
// server-side "an approver, not the requester" role check lives in the tenant
// layer that owns the tenant/role model; this method only performs the
// lifecycle transition once that check has passed.
func (s *Service) Approve(ctx context.Context, id, approver string) (Changeset, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if transErr := c.Transition(StatusDraft, s.now().Unix()); transErr != nil {
		return Changeset{}, transErr
	}
	c.Findings = s.validateScoped(ctx, c.ClusterID, c.Ops)
	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: approving request-changeset %s: %w", id, err)
	}
	s.appendAudit(ctx, approver, "changeset.approve", "success", id, nil)
	s.broadcastStatus(c)
	return c, nil
}

// UpdateDraft replaces a draft or validated changeset's ops (docs/api.md:
// "PUT /changesets/{id} — replace ops on a draft (revalidates)"). Editing a
// validated changeset invalidates it back to draft (its findings, computed
// against the old ops, no longer apply); the new ops are immediately
// revalidated against the current inventory snapshot regardless (same
// auto-validation-on-mutation behavior as Create), but — as with Create —
// only the explicit Validate call promotes StatusDraft to StatusValidated.
// It returns *ErrIllegalTransition if the changeset is not currently
// editable (Changeset.Editable).
func (s *Service) UpdateDraft(ctx context.Context, id, author string, title *string, ops []Op) (Changeset, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if !c.Editable() {
		return Changeset{}, &ErrIllegalTransition{From: c.Status, To: StatusDraft}
	}

	prevStatus := c.Status
	if c.Status == StatusValidated {
		if transErr := c.Transition(StatusDraft, s.now().Unix()); transErr != nil {
			return Changeset{}, transErr
		}
	}

	if ops == nil {
		ops = []Op{}
	}
	if err = s.sealOpSecrets(ops); err != nil {
		return Changeset{}, err
	}
	c.Ops = ops
	findings := s.validateScoped(ctx, c.ClusterID, ops)
	c.Findings = findings
	if title != nil {
		c.Title = *title
	}
	c.UpdatedAt = s.now().Unix()

	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: updating changeset %s: %w", id, err)
	}
	s.auditSafetyOverride(ctx, author, id, findings)
	if prevStatus != c.Status {
		s.broadcastStatus(c)
	}
	return c, nil
}

// Discard transitions a draft or validated changeset to StatusDiscarded
// (docs/api.md: "DELETE /changesets/{id} — discard draft"), audits it, and
// broadcasts the resulting `changeset.status` event. It returns
// *ErrIllegalTransition for a changeset that is no longer a draft (already
// applying, or a terminal historical record).
func (s *Service) Discard(ctx context.Context, id, author string) error {
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if transErr := c.Transition(StatusDiscarded, s.now().Unix()); transErr != nil {
		return transErr
	}
	row, err := toStoreRow(c)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return fmt.Errorf("change: discarding changeset %s: %w", id, err)
	}
	// T-1805: deleting a changeset (docs/api.md: "DELETE /changesets/{id} —
	// discard draft") must not leave a credential behind. A discardable
	// changeset is a draft and so should never hold a sealed ticket at all —
	// which is precisely why the wipe is unconditional here rather than
	// conditional on a state check that could one day stop holding.
	s.wipeRevertTicket(ctx, id)
	s.appendAudit(ctx, author, "changeset.discard", "success", id, nil)
	s.broadcastStatus(c)
	return nil
}

// Validate re-runs the T-202 validation pipeline against id's current ops
// and the current inventory snapshot (docs/api.md: "POST /changesets/{id}/
// validate — re-run validation, returns findings"), persists the resulting
// Findings, and updates Status: a StatusDraft changeset with no
// error-severity findings is promoted to StatusValidated (matching
// changeset.go's StatusValidated doc comment: "the last validation run
// found no blocking errors against the ops as they stood at that time");
// conversely a StatusValidated changeset that now has an error (the
// snapshot moved since it was last validated) is demoted back to
// StatusDraft. It returns *ErrIllegalTransition if the changeset is not
// currently editable (Changeset.Editable) — validating an in-flight or
// terminal changeset doesn't mean anything.
func (s *Service) Validate(ctx context.Context, id, author string) (Changeset, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if !c.Editable() {
		return Changeset{}, &ErrIllegalTransition{From: c.Status, To: StatusValidated}
	}

	findings := s.validateScoped(ctx, c.ClusterID, c.Ops)
	c.Findings = findings
	clean := !hasError(findings)
	prevStatus := c.Status

	switch {
	case clean && c.Status == StatusDraft:
		if transErr := c.Transition(StatusValidated, s.now().Unix()); transErr != nil {
			return Changeset{}, transErr
		}
	case !clean && c.Status == StatusValidated:
		if transErr := c.Transition(StatusDraft, s.now().Unix()); transErr != nil {
			return Changeset{}, transErr
		}
	default:
		c.UpdatedAt = s.now().Unix()
	}

	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: validating changeset %s: %w", id, err)
	}

	result := "clean"
	if !clean {
		result = "errors"
	}
	s.appendAudit(ctx, author, "changeset.validate", result, id, map[string]any{"findingCount": len(findings)})
	s.auditSafetyOverride(ctx, author, id, findings)
	if prevStatus != c.Status {
		s.broadcastStatus(c)
	}
	return c, nil
}

// GetProtected returns the current onboarding-confirmed protected-interface
// config (docs/api.md-to-be: "minimal API to read/update" protected.json,
// per T-203's card). A missing file (onboarding hasn't run yet) is not an
// error — LoadProtectedConfig returns an empty config for that case.
func (s *Service) GetProtected(_ context.Context) (ProtectedConfig, error) {
	cfg, err := LoadProtectedConfig(s.protectedPath)
	if err != nil {
		return ProtectedConfig{}, fmt.Errorf("change: getting protected-interface config: %w", err)
	}
	return cfg, nil
}

// SuggestProtected computes the detection-suggested protected-interface set
// (T-203's "detection of protected interfaces per node" deliverable, wired
// to GET /protected-interfaces/suggest per audit-phase-2 F-14): the live
// inventory snapshot composed with the node's parsed corosync.conf through
// DetectProtected. corosync.conf lives on pmxcfs, so the local copy names
// every cluster node's ring addresses — detection is cluster-wide from one
// read. A missing or unreadable corosync.conf degrades to management-IP-only
// detection (a not-yet-clustered node has no corosync links to protect);
// that is a documented fallback, not an error, so this never fails the
// suggest endpoint — the admin reviews and corrects the suggestion during
// onboarding anyway.
func (s *Service) SuggestProtected(_ context.Context) ProtectedSet {
	cor, err := host.ReadCorosyncConf(s.corosyncPath)
	if err != nil {
		if !os.IsNotExist(errors.Unwrap(err)) && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("change: reading corosync.conf for protected-interface detection; falling back to management-IP-only detection", "error", err)
		}
		cor = nil
	}
	return DetectProtected(s.inventorySnapshot(), cor)
}

// MgmtStatus computes docs/api.md's `GET /protected-interfaces/status`
// response (T-702): the onboarding-confirmed protected set (falling back to
// live detection when protected.json is empty, per MgmtStatus's own doc
// comment) classified into roles and resolved into physical paths via
// internal/topology's shared resolver — the same computation `GET /topology`
// paints its mgmt/corosync/mgmt-path badges from (internal/api's
// handleTopology calls this exact method too), so the two surfaces can never
// disagree. Never fails on a missing/unreadable corosync.conf (falls back to
// management-IP-only classification, logged, same tolerance
// SuggestProtected already has) — only a broken protected.json read is a
// hard error.
func (s *Service) MgmtStatus(_ context.Context) (MgmtStatus, error) {
	cfg, err := LoadProtectedConfig(s.protectedPath)
	if err != nil {
		return MgmtStatus{}, fmt.Errorf("change: computing management-path status: %w", err)
	}

	cor, cerr := host.ReadCorosyncConf(s.corosyncPath)
	if cerr != nil {
		if !errors.Is(cerr, os.ErrNotExist) && !os.IsNotExist(errors.Unwrap(cerr)) {
			s.log.Warn("change: reading corosync.conf for management-path status; falling back to management-IP-only detection", "error", cerr)
		}
		cor = nil
	}

	snap := s.inventorySnapshot()
	resolved, bad := cfg.Resolve()

	source := "detected"
	stale := false
	roleRefs := DetectProtectedRoles(snap, cor)
	if len(resolved) > 0 {
		source = "confirmed"
		stale = protectedSetStale(roleRefs, resolved)
		roleRefs = classifyConfirmedRoles(snap, cor, resolved)
	}

	return MgmtStatus{
		Source:         source,
		Nodes:          topology.ResolveMgmtPaths(snap, roleRefs),
		BadRefs:        bad,
		StaleProtected: stale,
	}, nil
}

// protectedSetStale reports whether live detection (detected) names a
// carrier ref the onboarding-confirmed set (confirmed) does not contain —
// MgmtStatus.StaleProtected's definition (T-703: the post-commit
// protected-set refresh prompt's trigger, and the "declined the refresh"
// warning's condition).
func protectedSetStale(detected map[string][]topology.MgmtRoleRef, confirmed ProtectedSet) bool {
	for node, roleRefs := range detected {
		have := map[inventory.Ref]bool{}
		for _, ref := range confirmed[node] {
			have[ref] = true
		}
		for _, rr := range roleRefs {
			if !have[rr.Ref] {
				return true
			}
		}
	}
	return false
}

// SetProtected validates and persists a new protected-interface config
// (the onboarding confirmation/correction flow, docs/features/blueprints.md
// §3), stamping Version/UpdatedAt/UpdatedBy, and audits the change. It
// returns *ErrInvalidProtectedRef if any ref string in cfg.Nodes fails to
// parse — the API layer maps that to a 400, mirroring how a malformed op
// target is handled elsewhere in this package — or if a ref's embedded node
// doesn't match the map key it is filed under (audit-phase-2 F-15: a
// mis-filed ref would silently be validated against the wrong node's
// address table, which is worse than rejecting it loudly).
func (s *Service) SetProtected(ctx context.Context, author string, cfg ProtectedConfig) (ProtectedConfig, error) {
	set, bad := cfg.Resolve()
	if len(bad) > 0 {
		return ProtectedConfig{}, &ErrInvalidProtectedRef{Refs: bad}
	}
	for node, refs := range set {
		for _, ref := range refs {
			if ref.Node != node {
				bad = append(bad, ref.String())
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad) // set is a map — make the reported order stable
		return ProtectedConfig{}, &ErrInvalidProtectedRef{Refs: bad}
	}
	if cfg.Nodes == nil {
		cfg.Nodes = map[string][]string{}
	}
	cfg.Version = protectedConfigVersion
	cfg.UpdatedAt = s.now().Unix()
	cfg.UpdatedBy = author

	if err := SaveProtectedConfig(s.protectedPath, cfg); err != nil {
		return ProtectedConfig{}, fmt.Errorf("change: setting protected-interface config: %w", err)
	}

	nodeCount, refCount := len(cfg.Nodes), 0
	for _, refs := range cfg.Nodes {
		refCount += len(refs)
	}
	s.appendAudit(ctx, author, "protected.update", "success", "", map[string]any{"nodeCount": nodeCount, "refCount": refCount})
	return cfg, nil
}

// sealOpSecrets seals any plaintext secret embedded in ops (today only a
// wg.peer.add preshared key) into its sealed at-rest field and clears the
// plaintext, so it never reaches toStoreRow's ops_json or a read response
// (Finding 1 / docs/security.md's WireGuard credential-storage note). Called
// at stage/create time from Create and UpdateDraft, before persistence and
// validation, mutating the caller's op params in place (Op.Params holds a
// *WgPeerAddParams pointer).
//
// Idempotent: an op already carrying only the sealed form — a GET->PUT
// round-trip re-submitting it, or an internally-built op — has an empty
// PresharedKey and is left untouched. Fails closed: a plaintext secret with no
// configured Sealer is an error, so a misconfigured daemon can never silently
// persist a peer secret in the clear.
func (s *Service) sealOpSecrets(ops []Op) error {
	for _, op := range ops {
		p, ok := op.Params.(*WgPeerAddParams)
		if !ok || p.PresharedKey == "" {
			continue
		}
		if s.sealer == nil {
			return fmt.Errorf("change: refusing to persist a plaintext WireGuard preshared key for op %s with no configured secret cipher", op.Type)
		}
		sealed, err := s.sealer.Encrypt([]byte(p.PresharedKey))
		if err != nil {
			return fmt.Errorf("change: sealing preshared key for op %s: %w", op.Type, err)
		}
		p.PresharedKeyEnc = sealed
		p.PresharedKey = ""
	}
	return nil
}

// wgTunnelCarriers resolves the tunnelID->carrier map TouchesMgmtPath needs to
// flag carrier-less wg ops (wg.peer.*, wg.tunnel.delete, carrier-less update)
// on an existing management-path tunnel — used by the scheduling gate so an
// unattended scheduled such op is caught the same way the API interlock is.
// Nil-safe and soft-fail: no WgCarrierSource, or a failed read, yields nil (no
// carrier resolution), never blocking the gate on a transient store hiccup.
func (s *Service) wgTunnelCarriers(ctx context.Context) map[string]WgTunnelCarrier {
	if s.wgCarriers == nil {
		return nil
	}
	carriers, err := s.wgCarriers.TunnelCarriers(ctx)
	if err != nil {
		s.log.Warn("change: resolving WireGuard tunnel carriers for mgmt-path gate; treating as none", "error", err)
		return nil
	}
	return carriers
}

func (s *Service) appendAudit(ctx context.Context, username, action, result, changesetID string, detail map[string]any) {
	var detailJSON sql.NullString
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	entry := store.AuditEntry{
		At:          s.now().Unix(),
		Username:    username,
		Action:      action,
		Result:      result,
		ChangesetID: sql.NullString{String: changesetID, Valid: changesetID != ""},
		DetailJSON:  detailJSON,
	}
	if _, err := s.audit.Append(ctx, entry); err != nil {
		s.log.Error("change: appending audit entry", "action", action, "changeset_id", changesetID, "error", err)
	}
}

// statusEvent is the wire shape of a changeset.status push, per
// docs/api.md's WebSocket section: `{id, status, confirmDeadline?}`, plus
// the flat "event" field every hub-broadcast message in this codebase
// carries (see internal/topology/hub.go's deltaEvent).
type statusEvent struct {
	ConfirmDeadline *int64 `json:"confirmDeadline,omitempty"`
	Event           string `json:"event"`
	ID              string `json:"id"`
	Status          string `json:"status"`
}

func (s *Service) broadcastStatus(c Changeset) {
	if s.ws == nil {
		return
	}
	evt := statusEvent{Event: "changeset.status", ID: c.ID, Status: string(c.Status), ConfirmDeadline: c.ConfirmDeadline}
	data, err := json.Marshal(evt)
	if err != nil {
		s.log.Error("change: marshaling changeset.status event", "error", err)
		return
	}
	s.ws.Broadcast(topicChangesets, data)
}

// toStoreRow converts the typed aggregate to store.ChangesetRepo's
// flat/JSON-string row shape.
func toStoreRow(c Changeset) (store.Changeset, error) {
	opsJSON, err := json.Marshal(c.Ops)
	if err != nil {
		return store.Changeset{}, fmt.Errorf("change: marshaling ops for changeset %s: %w", c.ID, err)
	}
	row := store.Changeset{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status), ClusterID: c.ClusterID,
		Origin: c.Origin, OriginTokenID: c.OriginTokenID,
		OpsJSON: string(opsJSON), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.Findings != nil {
		findingsJSON, err := json.Marshal(c.Findings)
		if err != nil {
			return store.Changeset{}, fmt.Errorf("change: marshaling findings for changeset %s: %w", c.ID, err)
		}
		row.FindingsJSON = sql.NullString{String: string(findingsJSON), Valid: true}
	}
	if len(c.Plan) > 0 {
		row.PlanJSON = sql.NullString{String: string(c.Plan), Valid: true}
	}
	if len(c.ApplyLog) > 0 {
		row.ApplyLogJSON = sql.NullString{String: string(c.ApplyLog), Valid: true}
	}
	if c.ConfirmDeadline != nil {
		row.ConfirmDeadline = sql.NullInt64{Int64: *c.ConfirmDeadline, Valid: true}
	}
	return row, nil
}

// fromStoreRow is toStoreRow's inverse.
func fromStoreRow(row store.Changeset) (Changeset, error) {
	var ops []Op
	if err := json.Unmarshal([]byte(row.OpsJSON), &ops); err != nil {
		return Changeset{}, fmt.Errorf("change: decoding stored ops for changeset %s: %w", row.ID, err)
	}
	c := Changeset{
		ID: row.ID, Title: row.Title, Author: row.Author, Status: Status(row.Status), ClusterID: row.ClusterID,
		Origin: row.Origin, OriginTokenID: row.OriginTokenID,
		Ops: ops, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.FindingsJSON.Valid {
		if err := json.Unmarshal([]byte(row.FindingsJSON.String), &c.Findings); err != nil {
			return Changeset{}, fmt.Errorf("change: decoding stored findings for changeset %s: %w", row.ID, err)
		}
	}
	if row.PlanJSON.Valid {
		c.Plan = json.RawMessage(row.PlanJSON.String)
	}
	if row.ApplyLogJSON.Valid {
		c.ApplyLog = json.RawMessage(row.ApplyLogJSON.String)
	}
	if row.ConfirmDeadline.Valid {
		d := row.ConfirmDeadline.Int64
		c.ConfirmDeadline = &d
	}
	// T-1805: the sealed ticket's expiry (a bound, not the credential — see
	// Changeset.RevertTicketExpiresAt). toStoreRow deliberately has no inverse
	// for this: SealRevertTicket/WipeRevertTicket are the only writers, so an
	// ordinary persist can never clobber or resurrect a ticket.
	if row.RevertTicketExpiresAt.Valid {
		c.RevertTicketExpiresAt = row.RevertTicketExpiresAt.Int64
	}
	return c, nil
}
