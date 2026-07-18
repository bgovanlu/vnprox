package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
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
		// Always a non-nil slice: findings.Finding.Nodes has no `omitempty`,
		// so a nil here serializes as JSON `null`, which crashes the
		// frontend's `for (const n of f.nodes)` in web/src/findings/filters.ts
		// (found via T-806's e2e run on the probe producer, which had the same
		// latent bug). IPAM conflicts are subnet-scoped, not node-scoped, so
		// the array is legitimately empty — but it must be `[]`, never `null`.
		Nodes:    []string{},
		DocsLink: ipamConflictDocsLink,
	}
}

// probeFindingsAdapter adapts T-806's persisted *store.SimDivergenceRepo
// into the unified findings stream (findings.ProbeProvider). This
// composition-root conversion is what keeps internal/store from importing
// internal/findings (the same decoupling ipamFindingsAdapter provides for
// internal/ipam). Nil-safe: a nil repo (store failed to open — server.go
// treats that as fatal today, but keeping this adapter nil-safe matches
// every other producer seam's own defensive convention) contributes no
// findings.
type probeFindingsAdapter struct {
	repo   *store.SimDivergenceRepo
	logger *slog.Logger
}

// simDivergenceDeepLink builds T-806's DocsLink for a persisted
// sim_divergence finding. Unlike every other producer, this finding's
// DocsLink is deliberately not a docs page: it's the deep link back into
// the path simulator (docs/features/topology.md §2's path overlay)
// carrying the exact src/dst/proto/port tuple, per T-806's deliverable ("a
// docsLink/deep-link carrying the exact src/dst/proto/port tuple back into
// the simulator") — a documented deviation from docs/api.md's usual
// DocsLink contract ("a relative path into this repo's docs"), flagged
// here and in docs/api.md's findings section. Query param names mirror
// web/src/simulator/urlState.ts's
// encodeSimState exactly (srcKind/srcRef/srcIp, dstKind/dstRef/dstIp,
// proto, port) so the frontend's existing decodeSimState reads it with no
// new parsing logic — the same URL-state mechanism "Copy link" and
// "Trace path" already share (SimulatorPage.tsx).
func simDivergenceDeepLink(f store.SimDivergenceFinding) string {
	q := url.Values{}
	q.Set("srcKind", "guest-nic")
	q.Set("srcRef", f.SrcRef)
	q.Set("dstKind", f.DstKind)
	switch f.DstKind {
	case "guest-nic":
		q.Set("dstRef", f.DstRef)
	case "ip":
		q.Set("dstIp", f.DstIP)
	}
	if f.Proto != "" {
		q.Set("proto", f.Proto)
	}
	if f.Port > 0 {
		q.Set("port", strconv.Itoa(f.Port))
	}
	return "/tools?" + q.Encode()
}

// probeDivergenceToFinding maps one persisted SimDivergenceFinding row to
// the unified Finding shape: source probe, check sim_divergence, id reused
// verbatim from the stored row (already the stable content-derived key
// internal/api's simDivergenceTupleKey computed when it was written).
// Never fixable — a live/simulated disagreement is a fact to investigate,
// not something with a computable config patch (docs/features/firewall.md
// §5/§6's honesty contract: a divergence is never presented as a silent
// correction of the simulated verdict, which a "fix" affordance would
// imply). Severity is warning uniformly: this producer can't know from the
// tuple alone whether the disagreement is security-relevant (an allow
// verdict that's actually unreachable) or merely a false negative (a deny
// verdict that's actually reachable) — "worth a look", not an escalated
// claim either way.
func probeDivergenceToFinding(f store.SimDivergenceFinding) findings.Finding {
	// Nodes is always a non-nil slice (empty when the src ref doesn't parse,
	// which should never happen for a row this package itself wrote) —
	// findings.Finding.Nodes has no `omitempty`, so a nil slice here would
	// serialize as JSON `null` instead of `[]`; found the hard way via this
	// task's own e2e run, which crashed web/src/findings/filters.ts's
	// `for (const n of f.nodes)` on exactly that (also hardened
	// defensively on the frontend side, but this producer shouldn't rely
	// on that alone). The src's own node is a meaningful value here (lets
	// the findings stream's node filter include probe findings too), not
	// just a placeholder.
	nodes := []string{}
	if ref, err := inventory.ParseRef(f.SrcRef); err == nil && ref.Node != "" {
		nodes = []string{ref.Node}
	}
	return findings.Finding{
		ID:       f.ID,
		Source:   findings.SourceProbe,
		Check:    "sim_divergence",
		Severity: findings.SeverityWarning,
		Detail:   f.Detail,
		Nodes:    nodes,
		Refs:     []string{f.SrcRef},
		DocsLink: simDivergenceDeepLink(f),
	}
}

func (a probeFindingsAdapter) Findings() []findings.Finding {
	if a.repo == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := a.repo.List(ctx)
	if err != nil {
		a.logger.Warn("findings: listing persisted sim_divergence findings", "error", err)
		return nil
	}
	out := make([]findings.Finding, 0, len(rows))
	for _, row := range rows {
		out = append(out, probeDivergenceToFinding(row))
	}
	return out
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

// scheduleMissedAdapter adapts a lazily-set *change.Service into
// findings.ScheduleMissedProvider (T-1103's schedule_missed health check),
// the exact same lazily-set pattern mgmtStatusAdapter above establishes and
// for the identical reason: server.go builds the findings.Engine (via
// setupFindings) before change.Service exists.
type scheduleMissedAdapter struct {
	svc *change.Service
	mu  sync.Mutex
}

func (a *scheduleMissedAdapter) set(svc *change.Service) {
	a.mu.Lock()
	a.svc = svc
	a.mu.Unlock()
}

// MissedSchedules implements findings.ScheduleMissedProvider. Returns nil
// (no findings, not an error) if called before set — mirrors
// mgmtStatusAdapter.MgmtStatus's identical degrade-before-ready contract.
func (a *scheduleMissedAdapter) MissedSchedules() []change.MissedSchedule {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.MissedSchedules(context.Background())
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

// fwAnalyticsAdapter is T-1006's lazily-set findings.FwAnalyticsProvider
// seam, the same pattern mgmtStatusAdapter above establishes: setupFindings
// builds the findings.Engine before *fwlog.Service exists (fwlogSvc is
// constructed later in server.go, after the change engine — see
// setupFwlog's own call site doc comment), so this adapter is wired in
// with its target unset and filled in via set() once fwlogSvc is built,
// always before the daemon starts serving requests or the findings RunLoop
// actually runs. Safe for concurrent use (the findings engine's own poll
// loop and an in-flight HTTP request could both call Analytics around
// startup).
type fwAnalyticsAdapter struct {
	svc *fwlog.Service
	mu  sync.Mutex
}

func (a *fwAnalyticsAdapter) set(svc *fwlog.Service) {
	a.mu.Lock()
	a.svc = svc
	a.mu.Unlock()
}

// Analytics implements findings.FwAnalyticsProvider. Returns a zero-value
// Analytics (no findings, not an error) if called before set — that can
// only happen if something evaluates findings before server.go finishes
// its startup sequence, which doesn't occur in production; mirrors
// mgmtStatusAdapter.MgmtStatus's identical degrade-before-ready contract.
func (a *fwAnalyticsAdapter) Analytics(now time.Time, window time.Duration, topN int) fwlog.Analytics {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return fwlog.Analytics{}
	}
	return svc.Analytics(now, window, topN)
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
// subnet/allocation conflicts (internal/ipam, adapted here), T-806's
// persisted sim_divergence findings (probeRepo, adapted here), and this
// package's own health checks over the same live graph metricsSampler
// already reads. ipamSvc/probeRepo are each nil-safe (degraded mode with no
// PVE client contributes no IPAM findings via ipamFindingsAdapter; a store
// that failed to open contributes no probe findings via
// probeFindingsAdapter).
//
// notifier is nil-safe (findings.Engine.Config.Notifier accepts nil,
// disabling the notification hook entirely — the P1 half of this task's
// deliverable is present but harmless to omit if, say, the PVE client
// failed to construct).
func setupFindings(graph *inventory.Graph, driftSvc findings.DriftProvider, topoSvc *topology.Service, metricsSampler *metrics.Sampler, mgmtSvc findings.MgmtProvider, corosyncSvc findings.CorosyncProvider, fwAnalyticsSvc findings.FwAnalyticsProvider, scheduleSvc findings.ScheduleMissedProvider, latMeshSvc findings.LatMeshProvider, webhookRepo *store.WebhookRepo, notifier findings.Notifier, ws findingsBroadcaster, ipamSvc *ipam.Service, probeRepo *store.SimDivergenceRepo, logger *slog.Logger) *findings.Engine {
	return findings.New(findings.Config{
		Graph:       graph,
		Drift:       driftSvc,
		LLDP:        topoSvc,
		IPAM:        ipamFindingsAdapter{ipam: ipamSvc, logger: logger},
		Probe:       probeFindingsAdapter{repo: probeRepo, logger: logger},
		Metrics:     metricsSampler,
		Mgmt:        mgmtSvc,
		Corosync:    corosyncSvc,
		FwAnalytics: fwAnalyticsSvc,
		Schedule:    scheduleSvc,
		// T-1104: the webhook_unhealthy health check, computed live from
		// webhookRepo's own consecutive_failures column — see
		// automation.go's webhookHealthAdapter doc comment.
		Webhooks: webhookHealthAdapter{repo: webhookRepo, logger: logger},
		LatMesh:  latMeshSvc,
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

// multiNotifier fans one finding transition out to every wrapped Notifier
// (T-1005: PVE's notification-target system alongside vnprox's own webhook
// routing, independent of each other per that task's card). It is a
// composition-root-only concern — a plain implementation of the existing
// findings.Notifier interface, added here rather than in
// internal/findings/notify.go, so Engine's own notification-firing/
// once-per-transition logic (notify.go's evaluateNotifications) is
// untouched by this task: Engine still calls exactly one Notifier, this
// type is just what that one Notifier fans out to. Every wrapped
// notifier's error is logged by its own Notify implementation already
// (PVENotifier/WebhookNotifier both do); this type additionally collects
// the first one so Engine.fireNotification's own log line still fires.
type multiNotifier struct {
	notifiers []findings.Notifier
}

// newMultiNotifier drops any nil entries, so a caller can pass every
// candidate notifier unconditionally (mirrors setupFindings' own "nil
// dependency -> that producer contributes nothing" convention). Returns
// nil (not an empty multiNotifier) if every candidate was nil, so
// findings.Config's own "Notifier == nil disables the hook entirely" check
// still short-circuits cleanly instead of looping over zero notifiers on
// every cycle.
func newMultiNotifier(notifiers ...findings.Notifier) findings.Notifier {
	var live []findings.Notifier
	for _, n := range notifiers {
		if n != nil {
			live = append(live, n)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if len(live) == 1 {
		return live[0]
	}
	return multiNotifier{notifiers: live}
}

func (m multiNotifier) Notify(ctx context.Context, f findings.Finding, kind findings.TransitionKind) error {
	var firstErr error
	for _, n := range m.notifiers {
		if err := n.Notify(ctx, f, kind); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// alertSecretCipher is the subset of *store.SessionCipher
// alertRuleProviderAdapter needs — declared as an interface (the same seam
// pattern every other cross-package dependency in this file uses) so tests
// can substitute a fake cipher.
type alertSecretCipher interface {
	Decrypt(sealed []byte) ([]byte, error)
}

// alertRuleStore is the subset of *store.AlertRuleRepo
// alertRuleProviderAdapter needs.
type alertRuleStore interface {
	List(ctx context.Context) ([]store.AlertRule, error)
}

// alertRuleProviderAdapter adapts *store.AlertRuleRepo (plus the session
// cipher) into findings.AlertRuleProvider (T-1005's webhook.go seam) —
// this is the decoupling conversion internal/findings/webhook.go's own doc
// comment describes: internal/findings never imports internal/store, this
// package's composition root does the decrypt-and-adapt. A rule whose
// secret fails to decrypt (a corrupt row, or a key rotated out from under
// an existing rule) is logged and skipped for this cycle rather than
// failing the whole notification fan-out.
type alertRuleProviderAdapter struct {
	repo   alertRuleStore
	cipher alertSecretCipher
	logger *slog.Logger
}

func (a alertRuleProviderAdapter) AlertRules(ctx context.Context) ([]findings.AlertRule, error) {
	rows, err := a.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("findings: listing alert rules: %w", err)
	}
	out := make([]findings.AlertRule, 0, len(rows))
	for _, row := range rows {
		secret := ""
		if len(row.TargetSecretEnc) > 0 {
			plaintext, decErr := a.cipher.Decrypt(row.TargetSecretEnc)
			if decErr != nil {
				a.logger.Warn("findings: decrypting alert rule secret, skipping rule this cycle", "rule_id", row.ID, "error", decErr)
				continue
			}
			secret = string(plaintext)
		}
		out = append(out, findings.AlertRule{
			ID: row.ID, Name: row.Name, Enabled: row.Enabled,
			SourceFilter: row.SourceFilter, SeverityFilter: row.SeverityFilter,
			TargetKind: row.TargetKind, TargetURL: row.TargetURL, TargetSecret: secret,
		})
	}
	return out, nil
}

// alertDeliveryStore is the subset of *store.AlertDeliveryRepo
// alertDeliveryRecorderAdapter needs.
type alertDeliveryStore interface {
	Insert(ctx context.Context, d store.AlertDelivery) error
}

// alertDeliveryRecorderAdapter adapts *store.AlertDeliveryRepo into
// findings.DeliveryRecorder, assigning the storage-layer ID
// (store.NewULID()) that findings.AlertDelivery deliberately doesn't carry
// (see that type's doc comment).
type alertDeliveryRecorderAdapter struct {
	repo alertDeliveryStore
}

func (a alertDeliveryRecorderAdapter) RecordDelivery(ctx context.Context, d findings.AlertDelivery) error {
	return a.repo.Insert(ctx, store.AlertDelivery{
		ID: store.NewULID(), RuleID: d.RuleID, FindingID: d.FindingID,
		At: d.At.Unix(), Attempt: d.Attempt, Status: d.Status, Error: d.Error,
	})
}

// findingEventStore is the subset of *store.FindingEventRepo
// findingEventRecorderAdapter needs.
type findingEventStore interface {
	Insert(ctx context.Context, e store.FindingEvent) error
}

// findingEventRecorderAdapter adapts *store.FindingEventRepo into
// findings.FindingEventRecorder (T-1007's finding_events writer seam) —
// the same decoupling conversion alertDeliveryRecorderAdapter provides for
// *store.AlertDeliveryRepo: internal/findings never imports internal/store,
// this package's composition root does the conversion.
type findingEventRecorderAdapter struct {
	repo findingEventStore
}

func (a findingEventRecorderAdapter) RecordFindingEvent(ctx context.Context, findingID string, at int64, transition string) error {
	return a.repo.Insert(ctx, store.FindingEvent{FindingID: findingID, At: at, Transition: transition})
}

// setupFindingEventsNotifier builds T-1007's finding_events Notifier over
// the app store's finding_events repo — composed alongside PVENotifier/
// WebhookNotifier via newMultiNotifier below, not a replacement for either.
// Never nil, mirroring setupAlertWebhookNotifier's own "always constructed"
// convention: an empty finding_events table is a correct, harmless
// starting state, not a reason to skip wiring the writer.
func setupFindingEventsNotifier(repo *store.FindingEventRepo, logger *slog.Logger) findings.Notifier {
	return findings.NewFindingEventsNotifier(findingEventRecorderAdapter{repo: repo}, logger)
}

// setupAlertWebhookNotifier builds T-1005's webhook Notifier from the app
// store's alert_rules/alert_deliveries repos and the session-secret cipher
// (docs/security.md's AES-256-GCM pattern, reused verbatim rather than a
// second cipher — see setupAuth's doc comment). Never nil: even with zero
// configured rules, WebhookNotifier.Notify is a correct, harmless no-op
// each cycle (matches PVENotifier's own "always constructed, does nothing
// if nothing is configured" shape when there are no enabled targets).
func setupAlertWebhookNotifier(alertRules *store.AlertRuleRepo, alertDeliveries *store.AlertDeliveryRepo, cipher *store.SessionCipher, logger *slog.Logger) findings.Notifier {
	return findings.NewWebhookNotifier(findings.WebhookNotifierConfig{
		Rules:    alertRuleProviderAdapter{repo: alertRules, cipher: cipher, logger: logger},
		Recorder: alertDeliveryRecorderAdapter{repo: alertDeliveries},
		Logger:   logger,
	})
}
