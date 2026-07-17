package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// ipamFindingsAdapter adapts internal/ipam's conflict output into the
// unified findings stream (findings.IPAMProvider). This composition-root
// conversion is what keeps internal/ipam from importing internal/findings
// (the same decoupling dhcpAllocationsAdapter provides for the change
// engine). Nil-safe: a nil ipam service (degraded mode — no PVE client)
// contributes no findings.
type ipamFindingsAdapter struct {
	ipam   *ipam.Service
	logger *slog.Logger
}

// ipamConflictDocsLink is the remediation pointer for an IPAM conflict —
// they carry no computed fix op, so (like the other non-fixable producers)
// they link to the docs instead (docs/features/monitoring.md §5's
// "remediation ... docs link otherwise").
const ipamConflictDocsLink = "docs/features/ipam.md#2-conflicts"

func (a ipamFindingsAdapter) Findings() []findings.Finding {
	if a.ipam == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conflicts, err := a.ipam.Conflicts(ctx)
	if err != nil {
		a.logger.Warn("findings: computing IPAM conflicts", "error", err)
		return nil
	}
	out := make([]findings.Finding, 0, len(conflicts))
	for _, sc := range conflicts {
		out = append(out, ipamConflictToFinding(sc))
	}
	return out
}

// ipamConflictToFinding maps one IPAM conflict to a unified Finding: source
// ipam, check = the conflict type (duplicate_ip / observed_unallocated /
// allocated_dark), the same error|warning|info severity vocabulary, and a
// content-derived stable id (type, subnet, sorted addresses) so re-scanning
// unchanged state reproduces byte-identical ids (Engine's change/notify
// tracking depends on it). Not fixable (no computed op patch), so a docs
// link is attached.
func ipamConflictToFinding(sc ipam.SubnetConflict) findings.Finding {
	c := sc.Conflict
	ips := append([]string(nil), c.IPs...)
	sort.Strings(ips)
	detail := c.Message
	if c.Suggestion != "" {
		detail = c.Message + " — " + c.Suggestion
	}
	return findings.Finding{
		ID:       "ipam:" + c.Type + "|" + sc.CIDR + "|" + strings.Join(ips, ","),
		Source:   findings.SourceIPAM,
		Check:    c.Type,
		Severity: c.Severity,
		Detail:   detail,
		DocsLink: ipamConflictDocsLink,
	}
}

// mgmtStatusAdapter adapts a lazily-set *change.Service into
// findings.MgmtProvider (T-702's mgmt_single_path health check). server.go
// constructs the findings.Engine (via setupFindings) before change.Service
// exists — the change engine needs pieces the findings engine doesn't
// (cluster node/timer agents, snapshot/blob repos) — so this adapter is
// wired in with its target unset and filled in once changeSvc is built
// (server.go, right after change.NewService succeeds), always before the
// daemon starts serving requests or the findings RunLoop actually runs.
// Safe for concurrent use (the findings engine's own poll loop and an
// in-flight HTTP request could both call MgmtStatus around startup).
type mgmtStatusAdapter struct {
	svc *change.Service
	mu  sync.Mutex
}

func (a *mgmtStatusAdapter) set(svc *change.Service) {
	a.mu.Lock()
	a.svc = svc
	a.mu.Unlock()
}

// MgmtStatus implements findings.MgmtProvider. Returns a zero-value status
// (no findings, not an error) if called before set — that can only happen
// if something evaluates findings before server.go finishes its startup
// sequence, which doesn't occur in production; a nil MgmtStatus.Nodes map
// simply yields zero mgmt_single_path findings that cycle, same as the
// nil-provider case checkMgmtSinglePath already documents.
func (a *mgmtStatusAdapter) MgmtStatus() (change.MgmtStatus, error) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return change.MgmtStatus{}, nil
	}
	return svc.MgmtStatus(context.Background())
}

// corosyncStatusAdapter adapts a host.Reader + a localNode closure into
// findings.CorosyncProvider (T-803's corosync_link_degraded health check).
//
// **Scope note for the next agent**: this reports only the *local* node's
// ring status, via the same host.NewReal() instance realHost already is —
// Real.CorosyncStatus, like every other Real method, only ever serves its
// own node (see internal/host's Reader doc comment). Fanning this check out
// to every cluster peer's own ring status would need a new peer API route
// (mirroring Services/Links' own peer.Client + peer.Server methods) that
// T-803's task card scope did not include; flagged here (and in this task's
// completion report) as a deliberate, documented gap rather than a silent
// one — a multi-node cluster today only ever sees *this* daemon's own
// node's corosync health through this check, not every peer's.
type corosyncStatusAdapter struct {
	host      host.Reader
	localNode func() string
	logger    *slog.Logger
}

// CorosyncStatus implements findings.CorosyncProvider. Returns (nil, nil) —
// not an error — before the local node is known yet, or when the node runs
// no corosync at all (errors.Is(err, host.ErrCorosyncUnavailable): a
// single, not-yet-clustered node has nothing to report, the same clean
// degradation ErrFRRUnavailable already gets), so a fresh/single-node
// daemon never spams a spurious health finding or notification at startup.
func (a corosyncStatusAdapter) CorosyncStatus() (map[string][]host.RingStatus, error) {
	node := a.localNode()
	if node == "" || a.host == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := a.host.CorosyncStatus(ctx, node)
	if err != nil {
		if errors.Is(err, host.ErrCorosyncUnavailable) {
			return nil, nil
		}
		a.logger.Warn("findings: reading corosync ring status", "node", node, "error", err)
		return nil, nil
	}
	rings, err := host.ParseCorosyncStatus(raw)
	if err != nil {
		a.logger.Warn("findings: parsing corosync ring status", "node", node, "error", err)
		return nil, nil
	}
	if len(rings) == 0 {
		return nil, nil
	}
	return map[string][]host.RingStatus{node: rings}, nil
}

// topicFindings is the WS subscribe topic name for T-602's
// `findings.changed` event — the unified-stream counterpart of
// cmd/vnproxd/drift.go's topicDrift, added rather than replacing it (any
// existing subscriber to "drift" keeps working unchanged).
const topicFindings = "findings"

// findingsChangedEvent is docs/api.md's documented `findings.changed
// {count}` WS event payload, the same envelope shape driftChangedEvent uses.
type findingsChangedEvent struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// findingsBroadcaster is the same one-method WS seam driftBroadcaster uses.
type findingsBroadcaster interface {
	Broadcast(topic string, payload []byte)
}

// setupFindings builds T-602's *findings.Engine composing driftSvc (T-305,
// unchanged), topoSvc's LLDP VLAN cross-check (T-302, unchanged), IPAM
// subnet/allocation conflicts (internal/ipam, adapted here), and this
// package's own health checks over the same live graph metricsSampler
// already reads. ipamSvc is nil-safe (degraded mode with no PVE client
// contributes no IPAM findings, via ipamFindingsAdapter).
//
// notifier is nil-safe (findings.Engine.Config.Notifier accepts nil,
// disabling the notification hook entirely — the P1 half of this task's
// deliverable is present but harmless to omit if, say, the PVE client
// failed to construct).
func setupFindings(graph *inventory.Graph, driftSvc findings.DriftProvider, topoSvc *topology.Service, metricsSampler *metrics.Sampler, mgmtSvc findings.MgmtProvider, corosyncSvc findings.CorosyncProvider, notifier findings.Notifier, ws findingsBroadcaster, ipamSvc *ipam.Service, logger *slog.Logger) *findings.Engine {
	return findings.New(findings.Config{
		Graph:    graph,
		Drift:    driftSvc,
		LLDP:     topoSvc,
		IPAM:     ipamFindingsAdapter{ipam: ipamSvc, logger: logger},
		Metrics:  metricsSampler,
		Mgmt:     mgmtSvc,
		Corosync: corosyncSvc,
		Logger:   logger,
		Notifier: notifier,
		OnChange: func(count int) {
			data, err := json.Marshal(findingsChangedEvent{Event: "findings.changed", Count: count})
			if err != nil {
				logger.Error("findings: marshaling findings.changed event", "error", err)
				return
			}
			ws.Broadcast(topicFindings, data)
		},
	})
}

// setupFindingsNotifier builds T-602's P1 notification hook: PVE's
// notification-target system (webhook/email/gotify), driven through
// internal/pve.Client — nil when pveClient itself is nil (collectors failed
// to initialize; see setupCollect's doc comment on why that's tolerated
// rather than fatal), mirroring every other "no PVE client -> that feature
// quietly doesn't exist" degradation in server.go.
func setupFindingsNotifier(pveClient *pve.Client, logger *slog.Logger) findings.Notifier {
	if pveClient == nil {
		return nil
	}
	return findings.NewPVENotifier(pveClient, logger)
}
