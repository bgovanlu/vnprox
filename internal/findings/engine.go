package findings

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// DefaultInterval is the unified findings cycle cadence — the same 30s
// cadence internal/drift.DefaultInterval uses, since Engine's own periodic
// job is just "recompute everything and check for a transition", not a new
// independent poll of anything (drift/lldp/health all read from data
// collectors already refresh independently on their own cadences).
const DefaultInterval = 30 * time.Second

// Config configures an Engine. Graph is required; every producer/Notifier
// field is independently optional (nil skips that producer, exactly like
// internal/api's route-mounting convention of "nil dependency -> that
// feature quietly doesn't exist" for a partially-wired daemon or test).
type Config struct {
	Drift   DriftProvider
	LLDP    LLDPProvider
	IPAM    IPAMProvider
	Metrics MetricsProvider
	// Mgmt is T-702's management-path status seam (change.Service.MgmtStatus
	// adapted via MgmtProvider), backing the mgmt_single_path health check.
	// Nil skips that check entirely, same degradation as every other
	// optional Config field.
	Mgmt MgmtProvider
	// Corosync is T-803's corosync ring-status seam, backing the
	// corosync_link_degraded health check. Nil skips that check entirely,
	// same degradation as every other optional Config field.
	Corosync        CorosyncProvider
	Notifier        Notifier
	Graph           *inventory.Graph
	Logger          *slog.Logger
	OnChange        func(count int)
	Now             func() time.Time
	NotifyThreshold string
	Thresholds      HealthThresholds
	Interval        time.Duration
}

// Engine composes drift/lldp/ipam producers with this package's own health
// checks into docs/features/monitoring.md §5's single findings stream, on
// demand (Findings) and on a periodic cycle (RunLoop) that drives both the
// WS change notification and the notification-hook transition detection
// (AC5).
type Engine struct {
	notifier    Notifier
	driftSvc    DriftProvider
	lldpSvc     LLDPProvider
	ipamSvc     IPAMProvider
	metricsSvc  MetricsProvider
	mgmtSvc     MgmtProvider
	corosyncSvc CorosyncProvider
	serviceDB   *debouncer
	services    *serviceStatusStore
	onChange    func(int)
	log         *slog.Logger
	notified    map[string]string
	lastIDs     map[string]bool
	now         func() time.Time
	bondDB      *debouncer
	lacpDB      *debouncer
	carrierDB   *debouncer
	errDropDB   *debouncer
	corosyncDB  *debouncer
	vxlanMTUDB  *debouncer
	graph       *inventory.Graph
	stpTracker  *stpBurstTracker
	pendingTr   *pendingTracker
	notifyMin   string
	thresholds  HealthThresholds
	interval    time.Duration
	mu          sync.Mutex
	lastEval    bool
}

// New builds an Engine from cfg.
func New(cfg Config) *Engine {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	th := cfg.Thresholds
	if th == (HealthThresholds{}) {
		th = DefaultThresholds
	}
	notifyMin := cfg.NotifyThreshold
	if notifyMin == "" {
		notifyMin = SeverityWarning
	}

	return &Engine{
		graph:       cfg.Graph,
		driftSvc:    cfg.Drift,
		lldpSvc:     cfg.LLDP,
		ipamSvc:     cfg.IPAM,
		metricsSvc:  cfg.Metrics,
		mgmtSvc:     cfg.Mgmt,
		corosyncSvc: cfg.Corosync,
		log:         logger,
		now:         now,
		onChange:    cfg.OnChange,
		notifier:    cfg.Notifier,
		notifyMin:   notifyMin,
		interval:    interval,
		thresholds:  th,
		bondDB:      newDebouncer(),
		lacpDB:      newDebouncer(),
		carrierDB:   newDebouncer(),
		errDropDB:   newDebouncer(),
		serviceDB:   newDebouncer(),
		corosyncDB:  newDebouncer(),
		vxlanMTUDB:  newDebouncer(),
		stpTracker:  newStpBurstTracker(),
		pendingTr:   newPendingTracker(),
		services:    newServiceStatusStore(),
		notified:    map[string]string{},
	}
}

// IngestServices records node's current systemd unit status (the seam
// collect.Config.OnServices feeds, once per node per host-loop tick — see
// health_service.go's doc comment). Safe to call before the engine has run
// a single cycle; checkServiceDown simply has nothing to report yet.
func (e *Engine) IngestServices(node string, status map[string]bool) {
	e.services.ingest(node, status)
}

// Findings runs every producer fresh and returns the combined,
// deterministically-ordered unified stream. Pure with respect to each
// producer's current state except for the stateful health checks' own
// hysteresis/first-seen bookkeeping (bond/carrier/threshold/stp-burst/
// stale-pending), which necessarily depends on prior Findings() calls — the
// same "debounced, not memoryless" contract AC3 requires.
func (e *Engine) Findings() []Finding {
	var out []Finding
	out = append(out, driftFindings(e.driftSvc)...)
	out = append(out, lldpFindings(e.lldpSvc)...)
	out = append(out, ipamFindings(e.ipamSvc)...)
	out = append(out, e.healthFindings()...)
	sortFindings(out)
	return out
}

// healthFindings runs every health_*.go check family against the current
// graph snapshot.
func (e *Engine) healthFindings() []Finding {
	if e.graph == nil {
		return nil
	}
	snap := e.graph.Snapshot()
	now := e.now()

	var out []Finding
	out = append(out, checkBondSlaveDown(snap, e.bondDB)...)
	out = append(out, checkLACPPartnerMismatch(snap, e.lacpDB)...)
	out = append(out, checkBridgeNoCarrier(snap, e.carrierDB)...)
	out = append(out, checkSTPTopologyBurst(snap, e.stpTracker, now)...)
	out = append(out, checkStalePendingInterfaces(snap, e.pendingTr, now)...)
	out = append(out, checkErrorDropRate(snap, e.metricsSvc, e.errDropDB, e.thresholds)...)
	out = append(out, checkServiceDown(e.services, e.serviceDB)...)
	out = append(out, checkMgmtSinglePath(e.mgmtSvc)...)
	out = append(out, checkVxlanUnderlayMTU(snap, e.vxlanMTUDB)...)
	out = append(out, checkOrphanVnet(snap)...)
	out = append(out, checkEvpnGwInconsistency(snap)...)
	out = append(out, checkTrunkUnusedVlans(snap)...)
	out = append(out, checkCorosyncLinkDegraded(e.corosyncSvc, e.corosyncDB)...)
	return out
}

// FixOps returns the computed fixing-changeset op patch for the finding
// with the given unified id, dispatching to the owning producer. Only
// drift-sourced findings currently have a computable fix (bridge-property
// harmonization, MTU alignment — T-305's two families); every other
// producer's findings are detection-only (a DocsLink instead — see each
// adapter/check's own doc comment for why).
func (e *Engine) FixOps(id string) (ops []change.Op, title string, ok bool) {
	driftID, isDrift := strings.CutPrefix(id, "drift:")
	if !isDrift || e.driftSvc == nil {
		return nil, "", false
	}
	return e.driftSvc.FixOps(driftID)
}

// RunLoop drives the periodic findings cycle on e.interval until ctx is
// cancelled, matching cmd/vnproxd's runGroup actor signature — the same
// shape internal/drift.Service.RunLoop uses. Each cycle: recompute
// Findings, fire OnChange iff the ID set changed since the previous cycle
// (WS event), and run the notification-transition check (AC5).
func (e *Engine) RunLoop(ctx context.Context) error {
	e.cycle(ctx)
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			e.cycle(ctx)
		}
	}
}

func (e *Engine) cycle(ctx context.Context) {
	findings := e.Findings()
	ids := make(map[string]bool, len(findings))
	for _, f := range findings {
		ids[f.ID] = true
	}

	e.mu.Lock()
	changed := !e.lastEval || !sameIDSet(e.lastIDs, ids)
	e.lastIDs = ids
	e.lastEval = true
	e.mu.Unlock()

	if changed {
		e.log.Info("findings: unified stream changed", "count", len(findings))
		if e.onChange != nil {
			e.onChange(len(findings))
		}
	}

	e.evaluateNotifications(ctx, findings)
}

func sameIDSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}
