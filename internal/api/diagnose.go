// diagnose.go implements T-1307's guided diagnosis ladder route:
// `POST /diagnose {targetRef, escalateToCapture?}` → one internal/diagnose.
// Result. Every step closure below composes an existing surface this
// package already wires elsewhere in this file set — internal/sim.Simulate
// (config-check, T-503), internal/probe.Run (live-probe, T-802/T-806),
// internal/guestinterior via fetchQEMUInterior/fetchLXCInterior
// (guest-interior, T-1304), fetchClusterConntrack (conntrack, T-1305), and
// CaptureService.Start (capture, T-1301) — none of that logic is
// reimplemented here, only sequenced (internal/diagnose.Ladder) and
// resolved against one target (resolveDiagnoseTarget below).
//
// Advisory only: the ladder's Verdict never auto-remediates.
// SuggestedFixRef (when present) always names an existing finding's own
// `POST /findings/{id}/fix` link — see correlateDiagnoseFindings.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/diagnose"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/guestinterior"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/probe"
	"github.com/bgovanlu/vnprox/internal/sim"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxDiagnoseBodyBytes bounds POST /diagnose's request body — a target ref
// plus one bool needs only a tiny cap, mirroring
// maxGuestInteriorToggleBodyBytes' reasoning for a similarly small body.
const maxDiagnoseBodyBytes = 4 << 10

// diagnoseStepNames is the ladder's registration order, part of the
// documented contract (docs/api.md's Diagnosis section) — T-1307's card
// names exactly these five: config check (simulator) → live probe
// (verify-live) → guest interior → conntrack → capture.
const (
	stepConfigCheck   = "config-check"
	stepLiveProbe     = "live-probe"
	stepGuestInterior = "guest-interior"
	stepConntrack     = "conntrack"
	stepCapture       = "capture"
)

// DiagnoseCapabilityChecker is the seam POST /diagnose's capture-escalation
// step uses to check — never enforce/403 on — the requesting session's own
// `capture` capability. T-1307's card: "a caller without capture simply
// gets that step marked skipped, reason stated, rather than a 403 for the
// whole ladder", unlike every other capture-gated route in this codebase
// (POST /captures itself 403s via auth.RequireCap(capCapture)). Checked
// with a type assertion (mirrors UsernameLookup/CSRFEnforcer's own
// pattern) rather than folded into AuthService, so existing AuthService
// test doubles don't need updating just because this one route added it.
// Implemented by cmd/vnproxd's authServiceAdapter via
// auth.IdentityFromContext + Identity.HasCap.
type DiagnoseCapabilityChecker interface {
	HasCap(ctx context.Context, cap string) bool
}

// diagnoseRequest is POST /diagnose's body (docs/api.md's Diagnosis
// section).
type diagnoseRequest struct {
	TargetRef         string `json:"targetRef"`
	EscalateToCapture bool   `json:"escalateToCapture,omitempty"`
}

// mountDiagnoseRoutes registers `POST /diagnose` (T-1307). Reuses the same
// Options fields the router already wires for T-503/T-802/T-806's
// simulator routes, T-1304's guest interior routes, T-1305's conntrack
// route, and T-1301's capture routes — this card adds no new Options
// field, only composes existing ones. Simulator is required (nil skips
// mounting: without an inventory snapshot no step can resolve a target at
// all); auth must additionally implement UsernameLookup (audit
// attribution, matching every other audited route's own requirement) or
// the route is not mounted. DiagnoseCapabilityChecker is optional — its
// absence simply means the capture step always treats the capture
// capability as not held (fail-safe: skip, never silently escalate).
func mountDiagnoseRoutes(r chi.Router, opts Options, auth AuthService) {
	if opts.Simulator == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	capChecker, _ := auth.(DiagnoseCapabilityChecker)

	ladder := buildDiagnoseLadder(opts, lookup, capChecker)

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/diagnose", handleDiagnose(ladder, opts.Findings, opts.Simulator, opts.ProbeAudit, lookup))
	})
}

// buildDiagnoseLadder registers the five canonical steps in T-1307's
// card order — a registration table, not a hardcoded sequence: a future
// card (Phase 14's WireGuard/edge diagnostics) appends a Step at its own
// call site, never touching internal/diagnose's own code.
func buildDiagnoseLadder(opts Options, lookup UsernameLookup, capChecker DiagnoseCapabilityChecker) *diagnose.Ladder {
	steps := []diagnose.Step{
		{Name: stepConfigCheck, Run: diagnoseConfigCheckStep(opts.Simulator)},
		{Name: stepLiveProbe, Run: diagnoseLiveProbeStep(opts.Simulator, opts.ProbeClients, opts.SimDivergence)},
		{Name: stepGuestInterior, Run: diagnoseGuestInteriorStep(opts.GuestInteriorToggles, opts.Simulator, opts.ProbeClients, opts.GuestInteriorHost, opts.GuestInteriorPeers, opts.GuestInteriorIPAM, opts.LocalNode)},
		{Name: stepConntrack, Run: diagnoseConntrackStep(opts.Conntrack, opts.PeerConntrack, opts.ConntrackGuests, opts.LocalNode, opts.Simulator)},
		{Name: stepCapture, Run: diagnoseCaptureStep(opts.Captures, lookup, capChecker)},
	}
	return diagnose.NewLadder(steps, nil)
}

// handleDiagnose implements `POST /diagnose`: runs the ladder, correlates
// the result against the live findings stream (linkedFindingIds/
// suggestedFixRef), audits one `diagnose.run` row summarizing the whole
// call PLUS one `diagnose.step` row per ladder step (T-2002 — closing the
// gap T-1307's own report flagged: "which step reached which conclusion is
// not recoverable afterwards"), and returns the diagnose.Result verbatim.
// The per-step rows carry the parent `diagnose.run` row's own audit id
// (`runId` in their detail JSON) so a reviewer can group them back
// together; this is additive to the run-level row, not a replacement —
// existing "browse by diagnose.run" queries are unaffected.
func handleDiagnose(ladder *diagnose.Ladder, findingsSvc FindingsService, graph SimulatorGraph, audit simulateVerifyAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDiagnoseBodyBytes))
		dec.DisallowUnknownFields()
		var req diagnoseRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "invalid diagnose request body")
			return
		}
		if req.TargetRef == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "targetRef is required")
			return
		}
		if _, err := inventory.ParseRef(req.TargetRef); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "targetRef must be a valid inventory ref (kind:node:id)")
			return
		}

		result := ladder.Run(r.Context(), diagnose.Request{TargetRef: req.TargetRef, EscalateToCapture: req.EscalateToCapture})

		if findingsSvc != nil && graph != nil {
			ids, fixRef := correlateDiagnoseFindings(findingsSvc, graph.Snapshot(), req.TargetRef)
			result.Verdict.LinkedFindingIDs = mergeSortedUniqueStrings(result.Verdict.LinkedFindingIDs, ids)
			if result.Verdict.SuggestedFixRef == "" {
				result.Verdict.SuggestedFixRef = fixRef
			}
		}

		runID := auditDiagnoseRun(r.Context(), audit, username, req.TargetRef, result)
		auditDiagnoseSteps(r.Context(), audit, username, req.TargetRef, runID, result)
		writeJSON(w, http.StatusOK, result)
	}
}

// auditDiagnoseRun appends one diagnose.run audit_log row per POST
// /diagnose call (docs/api.md's Audit section conventions) — result "ok"
// unless any step ended StatusError, mirroring auditSimulateVerify's own
// "result reflects whether anything actually failed" convention. audit ==
// nil (no audit repo wired, e.g. a bare test router) skips logging, the
// same degraded-mode treatment every other optional audit seam in this
// package gets. Returns the appended row's own audit id (0, ok if audit is
// nil or the append itself failed — Append's error is deliberately
// swallowed here exactly as every other audit call site in this package
// does, since a failed audit write must never fail the request it's
// auditing) so auditDiagnoseSteps can tag each per-step row with it.
func auditDiagnoseRun(ctx context.Context, audit simulateVerifyAuditor, username, targetRef string, result diagnose.Result) int64 {
	if audit == nil {
		return 0
	}
	resultStr := "ok"
	stepSummary := make(map[string]string, len(result.Steps))
	for _, s := range result.Steps {
		stepSummary[s.Name] = string(s.Status)
		if s.Status == diagnose.StatusError {
			resultStr = "error"
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"steps":              stepSummary,
		"verdictConfidence":  result.Verdict.Confidence,
		"escalatedToCapture": stepSummary[stepCapture] == string(diagnose.StatusRan),
	})
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "diagnose.run", Result: resultStr}
	entry.Target.String, entry.Target.Valid = targetRef, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detail), true
	id, _ := audit.Append(ctx, entry)
	return id
}

// auditDiagnoseSteps appends one diagnose.step audit_log row per ladder
// step (T-2002, closing the gap T-1307's own report flagged: a diagnose.run
// row alone tells a reviewer "5 steps ran, verdict X" but not which step
// concluded what — reconstructing that meant re-running the ladder and
// hoping the target's state hadn't changed). Additive to auditDiagnoseRun's
// single summary row, never a replacement: `runId` in each step row's
// detail JSON is that summary row's own audit id, so `GET /audit` can group
// a run's steps back together without a second correlation mechanism.
// `result` is "skipped"\|"error"\|"ok" (StatusSkipped/StatusError/StatusRan
// respectively) — a step that never applied to this target is not an
// error, and the audit row says so plainly rather than looking like every
// other "ok". audit == nil skips logging, matching auditDiagnoseRun.
func auditDiagnoseSteps(ctx context.Context, audit simulateVerifyAuditor, username, targetRef string, runID int64, result diagnose.Result) {
	if audit == nil {
		return
	}
	for _, s := range result.Steps {
		resultStr := "ok"
		switch s.Status {
		case diagnose.StatusError:
			resultStr = "error"
		case diagnose.StatusSkipped:
			resultStr = "skipped"
		case diagnose.StatusRan:
			// resultStr already "ok".
		}
		detail, _ := json.Marshal(map[string]any{
			"runId":   runID,
			"step":    s.Name,
			"status":  string(s.Status),
			"summary": s.Summary,
		})
		entry := store.AuditEntry{At: s.RanAt, Username: username, Action: "diagnose.step", Result: resultStr}
		entry.Target.String, entry.Target.Valid = targetRef, true
		entry.DetailJSON.String, entry.DetailJSON.Valid = string(detail), true
		_, _ = audit.Append(ctx, entry)
	}
}

// --- target resolution -----------------------------------------------

// diagnoseTarget is the resolved shape every step closure below shares —
// computed once per ladder step invocation from the live inventory
// snapshot (never cached across requests: the snapshot itself is already
// the daemon's own poll-cached view, per docs/architecture.md's
// PVE-is-source-of-truth invariant) rather than each step re-parsing
// targetRef independently.
type diagnoseTarget struct {
	srcNic       *inventory.GuestNic
	guest        *inventory.Guest
	ref          inventory.Ref
	node         string
	gatewayIP    string
	relevantRefs []string
}

// resolveDiagnoseTarget resolves targetRef against snap into a
// diagnoseTarget. A target ref of kind guest-nic is used directly as the
// diagnostic source; a bare guest ref picks that guest's own
// lowest-Key NIC deterministically (the map's own per-guest presence is
// its NIC entity — see docs/api.md's Guest interior section — so a
// "Diagnose this guest" action from an entity that only carries a Guest
// ref still gets a usable source). Any other kind (bridge/bond/vlan/
// sdn-vnet/...) resolves to an edge target with no srcNic/guest at all —
// config-check/live-probe/guest-interior steps below then self-report
// ineligible for it, never erroring.
func resolveDiagnoseTarget(snap inventory.Snapshot, targetRef string) (diagnoseTarget, error) {
	ref, err := inventory.ParseRef(targetRef)
	if err != nil {
		return diagnoseTarget{}, fmt.Errorf("resolving diagnose target: %w", err)
	}
	dt := diagnoseTarget{ref: ref, node: ref.Node, relevantRefs: []string{ref.String()}}

	ent, ok := snap.Get(ref)
	if !ok {
		return dt, nil
	}
	switch e := ent.(type) {
	case *inventory.GuestNic:
		dt.srcNic = e
		dt.relevantRefs = append(dt.relevantRefs, e.Guest.String())
		if g, ok := snap.Get(e.Guest); ok {
			if guest, ok := g.(*inventory.Guest); ok {
				dt.guest = guest
				dt.node = guest.Node
			}
		}
		if e.BridgeOrVnet != (inventory.Ref{}) {
			dt.relevantRefs = append(dt.relevantRefs, e.BridgeOrVnet.String())
			dt.gatewayIP = resolveDiagnoseGatewayIP(snap, e.BridgeOrVnet)
		}
	case *inventory.Guest:
		dt.guest = e
		dt.node = e.Node
		if nic := firstGuestNic(snap, ref); nic != nil {
			dt.srcNic = nic
			dt.relevantRefs = append(dt.relevantRefs, nic.String())
			if nic.BridgeOrVnet != (inventory.Ref{}) {
				dt.relevantRefs = append(dt.relevantRefs, nic.BridgeOrVnet.String())
				dt.gatewayIP = resolveDiagnoseGatewayIP(snap, nic.BridgeOrVnet)
			}
		}
	default:
		// Bridge/Bond/Vlan/SdnVnet/other edge kinds: node-scoped only,
		// no guest source — config-check/live-probe/guest-interior steps
		// self-report ineligible for these.
	}
	return dt, nil
}

// firstGuestNic picks guestRef's deterministic "primary" NIC — the lowest
// Key (e.g. "net0" before "net1") — for a bare guest target with no
// specific NIC named. Returns nil if the guest has no NICs the inventory
// snapshot currently carries.
func firstGuestNic(snap inventory.Snapshot, guestRef inventory.Ref) *inventory.GuestNic {
	var best *inventory.GuestNic
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || nic.Guest != guestRef {
			continue
		}
		if best == nil || nic.Key < best.Key {
			best = nic
		}
	}
	return best
}

// resolveDiagnoseGatewayIP resolves networkRef's configured default
// gateway, when known — a Bridge's own Gateway field directly, or (for an
// SdnVnet) the first non-empty Gateway among its SdnSubnets. Returns ""
// when no gateway is configured/known, which config-check/live-probe below
// treat as "nothing to check reachability against" (skipped, never an
// error) rather than guessing a target.
func resolveDiagnoseGatewayIP(snap inventory.Snapshot, networkRef inventory.Ref) string {
	ent, ok := snap.Get(networkRef)
	if !ok {
		return ""
	}
	switch e := ent.(type) {
	case *inventory.Bridge:
		return e.Gateway
	case *inventory.SdnVnet:
		for _, other := range snap.All() {
			sub, ok := other.(*inventory.SdnSubnet)
			if ok && sub.Vnet == e.ID && sub.Gateway != "" {
				return sub.Gateway
			}
		}
	}
	return ""
}

// --- step: config-check (internal/sim.Simulate, T-503) -----------------

// diagnoseConfigCheckStep simulates the target guest's path to its own
// network's configured default gateway (static analysis over inventory,
// byte-identical engine to POST /simulate/path) — the ladder's first,
// config-only rung.
func diagnoseConfigCheckStep(graph SimulatorGraph) diagnose.StepFunc {
	return func(ctx context.Context, req diagnose.Request) diagnose.Outcome {
		snap := graph.Snapshot()
		dt, err := resolveDiagnoseTarget(snap, req.TargetRef)
		if err != nil {
			return diagnose.Outcome{Err: err}
		}
		if dt.srcNic == nil {
			return diagnose.Outcome{SkipReason: "config check requires a guest source (target has no resolvable guest NIC)"}
		}
		if dt.gatewayIP == "" {
			return diagnose.Outcome{SkipReason: "no configured gateway is known for this target's network to check reachability against"}
		}

		res := sim.Simulate(sim.Input{Inventory: snap}, sim.Request{
			Src: sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: dt.srcNic.Ref},
			Dst: sim.Endpoint{Kind: sim.EndpointIP, IP: dt.gatewayIP}, Proto: probe.ProtoICMP,
		})
		return diagnose.Outcome{
			Eligible: true,
			Summary:  fmt.Sprintf("simulated path to gateway %s: %s", dt.gatewayIP, res.Verdict),
			Detail:   toSimulateResponse(res),
		}
	}
}

// --- step: live-probe (internal/probe.Run, T-802/T-806) ----------------

// diagnoseLiveProbeStep runs a real ICMP probe from the target guest to
// its own network's default gateway via the QEMU guest agent — the same
// engine POST /simulate/verify uses — and, when it diverges from the
// simulated verdict, persists the identical sim_divergence finding
// POST /simulate/verify itself would (recordDivergence, reused verbatim:
// re-running the identical tuple by hand later still upserts/clears the
// same row, never a second divergent computation path).
func diagnoseLiveProbeStep(graph SimulatorGraph, probeClients ProbeClientProvider, divergence simDivergenceRecorder) diagnose.StepFunc {
	return func(ctx context.Context, req diagnose.Request) diagnose.Outcome {
		if probeClients == nil {
			return diagnose.Outcome{SkipReason: "no live PVE session support is configured on this daemon"}
		}
		snap := graph.Snapshot()
		dt, err := resolveDiagnoseTarget(snap, req.TargetRef)
		if err != nil {
			return diagnose.Outcome{Err: err}
		}
		if dt.srcNic == nil {
			return diagnose.Outcome{SkipReason: "live probe requires a guest source (target has no resolvable guest NIC)"}
		}
		if dt.gatewayIP == "" {
			return diagnose.Outcome{SkipReason: "no configured gateway is known for this target's network to probe"}
		}
		node, vmid, errMsg := resolveQemuGuestNicOwner(snap, dt.srcNic.Ref)
		if errMsg != "" {
			return diagnose.Outcome{SkipReason: "live probe requires a qemu guest source with a reachable guest agent: " + errMsg}
		}
		client, ok := probeClients.ProbeClientFor(ctx)
		if !ok {
			return diagnose.Outcome{SkipReason: "no active PVE session available for a live probe"}
		}

		observed := probe.Run(ctx, client, probe.Request{Node: node, VMID: vmid, DstIP: dt.gatewayIP, Proto: probe.ProtoICMP})
		simRes := sim.Simulate(sim.Input{Inventory: snap}, sim.Request{
			Src: sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: dt.srcNic.Ref},
			Dst: sim.Endpoint{Kind: sim.EndpointIP, IP: dt.gatewayIP}, Proto: probe.ProtoICMP,
		})
		diverges := divergesFrom(simRes.Verdict, observed.Outcome)

		dst := endpointSpec{Kind: string(sim.EndpointIP), IP: dt.gatewayIP}
		recordDivergence(ctx, divergence, dt.srcNic.String(), dst, probe.ProtoICMP, 0, simRes.Verdict, observed, diverges)

		out := diagnose.Outcome{
			Eligible: true,
			Summary:  fmt.Sprintf("live probe to gateway %s: %s (simulated: %s)", dt.gatewayIP, observed.Outcome, simRes.Verdict),
			Detail: verifyResponse{
				Simulated: toSimulateResponse(simRes),
				Observed:  verifyObserved{Outcome: string(observed.Outcome), Detail: observed.Detail, ExecError: observed.ExecError},
				Diverges:  diverges,
			},
		}
		if diverges {
			out.FindingIDs = []string{simDivergenceTupleKey(dt.srcNic.String(), dst, probe.ProtoICMP, 0)}
		}
		return out
	}
}

// --- step: guest-interior (internal/guestinterior, T-1304) -------------

// diagnoseGuestInteriorStep reuses fetchQEMUInterior/fetchLXCInterior
// (guestinterior.go, same file GET /guests/{ref}/interior itself calls)
// verbatim — never eligible for a target with no owning guest (StatusSkip,
// AC1's own named example), and never eligible when the guest hasn't opted
// in via its interior toggle (matching GET /guests/{ref}/interior's own
// 404 interior_not_enabled honesty contract, translated to a skip here
// rather than an HTTP error).
func diagnoseGuestInteriorStep(toggles GuestInteriorToggleStore, graph GuestInteriorGraph, probeClients ProbeClientProvider, hostReader GuestInteriorHostReader, peers PeerContainerSource, ipamSvc GuestInteriorIPAMSource, localNode func() string) diagnose.StepFunc {
	return func(ctx context.Context, req diagnose.Request) diagnose.Outcome {
		if toggles == nil || graph == nil {
			return diagnose.Outcome{SkipReason: "guest interior inspector is not configured on this daemon"}
		}
		snap := graph.Snapshot()
		dt, err := resolveDiagnoseTarget(snap, req.TargetRef)
		if err != nil {
			return diagnose.Outcome{Err: err}
		}
		if dt.guest == nil {
			return diagnose.Outcome{SkipReason: "no guest interior for a non-guest target"}
		}

		guestRefStr := dt.guest.String()
		enabled, err := toggles.Get(ctx, guestRefStr)
		if err != nil {
			return diagnose.Outcome{Err: fmt.Errorf("reading guest interior toggle: %w", err)}
		}
		if !enabled {
			return diagnose.Outcome{SkipReason: "the guest interior inspector is not opted in for this guest"}
		}

		var view guestinterior.View
		switch dt.guest.Type {
		case "qemu":
			view, err = fetchQEMUInterior(ctx, probeClients, dt.guest)
		case "lxc":
			view, err = fetchLXCInterior(ctx, hostReader, peers, localNode, dt.guest)
		default:
			return diagnose.Outcome{SkipReason: "guest type has no interior read path (neither qemu nor lxc)"}
		}
		if err != nil {
			return diagnose.Outcome{Eligible: true, Summary: "guest interior read unavailable: " + err.Error()}
		}

		diff := computeIPAMDiff(ctx, ipamSvc, view.Addresses)
		return diagnose.Outcome{
			Eligible: true,
			Summary:  fmt.Sprintf("guest interior read (%s): %d interface(s), default gateway reachable=%v", view.Source, len(view.Interfaces), view.DefaultGatewayReachable),
			Detail:   toGuestInteriorResponse(view, diff),
		}
	}
}

// --- step: conntrack (fetchClusterConntrack, T-1305) --------------------

// diagnoseConntrackStep scopes GET /conntrack's own cluster-wide read to
// the target: by the owning guest's known IPs when the target resolves to
// a guest, or by the target's own node otherwise — the same "node-scoped
// via that entity's own node" convention docs/api.md's Conntrack section
// documents for the map's right-click entry point on a non-guest entity.
func diagnoseConntrackStep(local ConntrackLocalSource, peers PeerConntrackSource, guests ConntrackGuestResolver, localNode func() string, graph SimulatorGraph) diagnose.StepFunc {
	return func(ctx context.Context, req diagnose.Request) diagnose.Outcome {
		if local == nil {
			return diagnose.Outcome{SkipReason: "conntrack read is not configured on this daemon"}
		}
		snap := graph.Snapshot()
		dt, err := resolveDiagnoseTarget(snap, req.TargetRef)
		if err != nil {
			return diagnose.Outcome{Err: err}
		}

		items, partial, failed := fetchClusterConntrack(ctx, local, peers, localNode)

		var filtered []conntrackEntryResponse
		var scopeDesc string
		if dt.guest != nil {
			guestRefStr := dt.guest.String()
			ips := map[string]bool{}
			if guests != nil {
				if resolved, err := guests.GuestIPs(ctx, guestRefStr); err == nil {
					for _, ip := range resolved {
						ips[ip] = true
					}
				}
			}
			for _, e := range items {
				if ips[e.SrcIP] || ips[e.DstIP] {
					filtered = append(filtered, e)
				}
			}
			scopeDesc = "guest " + guestRefStr
		} else {
			for _, e := range items {
				if dt.node == "" || e.Node == dt.node {
					filtered = append(filtered, e)
				}
			}
			scopeDesc = "node " + dt.node
		}
		if filtered == nil {
			filtered = []conntrackEntryResponse{}
		}

		summary := fmt.Sprintf("%d connection(s) for %s", len(filtered), scopeDesc)
		if partial {
			summary += fmt.Sprintf(" (partial: %d node(s) unreachable)", len(failed))
		}
		return diagnose.Outcome{
			Eligible: true,
			Summary:  summary,
			Detail:   conntrackListResponse{Items: filtered, Partial: partial, FailedNodes: failed},
		}
	}
}

// --- step: capture (capture.Coordinator, T-1301) ------------------------

// diagnoseCaptureStep is the ladder's only step with a real side effect —
// starting a capture session — and is therefore gated twice, both in
// StepFunc itself rather than at the route level (T-1307's card: "a
// capture is never triggered silently ... a caller without capture simply
// gets that step marked skipped ... rather than a 403 for the whole
// ladder"): (1) req.EscalateToCapture must be explicitly true (AC2's
// regression: false — the default — never calls svc.Start at all, checked
// before anything else below), and (2) the requesting session must hold
// the `capture` capability (AC3), checked via capChecker rather than the
// route-level 403 every other /captures-family route uses.
func diagnoseCaptureStep(svc CaptureService, lookup UsernameLookup, capChecker DiagnoseCapabilityChecker) diagnose.StepFunc {
	return func(ctx context.Context, req diagnose.Request) diagnose.Outcome {
		if !req.EscalateToCapture {
			return diagnose.Outcome{SkipReason: "capture escalation was not requested for this ladder run"}
		}
		if svc == nil {
			return diagnose.Outcome{SkipReason: "packet capture is not configured on this daemon"}
		}
		if capChecker == nil || !capChecker.HasCap(ctx, capCapture) {
			return diagnose.Outcome{SkipReason: "the capture capability is not held by this session"}
		}
		username, _ := lookup.Username(ctx)

		group, err := svc.Start(ctx, capture.StartRequest{TargetRef: req.TargetRef, StartedBy: username})
		if err != nil {
			return diagnose.Outcome{Eligible: true, Summary: "capture could not be started: " + err.Error()}
		}
		return diagnose.Outcome{
			Eligible: true,
			Summary:  fmt.Sprintf("capture group %s started (%d session(s))", group.ID, len(group.Sessions)),
			Detail:   group,
		}
	}
}

// --- verdict finding correlation ----------------------------------------

// correlateDiagnoseFindings scans findingsSvc's current findings for any
// whose Refs overlap the target's own relevant refs (itself, its owning
// guest, and its attached bridge/vnet — resolveDiagnoseTarget's
// relevantRefs) — surfacing, e.g., a bondslave/LACP/STP finding on the
// exact bridge a diagnosed guest sits on, or a persisted sim_divergence
// finding from a prior /simulate/verify run of the same guest, even one
// this ladder run's own live-probe step didn't itself re-detect. Returns
// every matched finding id (sorted) and the first Fixable match's id as
// the suggested fix — this never computes a fix itself, only points at an
// existing one (docs/api.md's Findings section: POST /findings/{id}/fix
// already exists and is unchanged by this route).
func correlateDiagnoseFindings(svc FindingsService, snap inventory.Snapshot, targetRef string) (ids []string, fixRef string) {
	dt, err := resolveDiagnoseTarget(snap, targetRef)
	if err != nil {
		return nil, ""
	}
	want := make(map[string]bool, len(dt.relevantRefs))
	for _, ref := range dt.relevantRefs {
		want[ref] = true
	}

	var matched []findings.Finding
	for _, f := range svc.Findings() {
		for _, ref := range f.Refs {
			if want[ref] {
				matched = append(matched, f)
				break
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	out := make([]string, 0, len(matched))
	for _, f := range matched {
		out = append(out, f.ID)
		if fixRef == "" && f.Fixable {
			fixRef = f.ID
		}
	}
	return out, fixRef
}

// mergeSortedUniqueStrings merges a and b into one sorted, deduplicated,
// always-non-nil slice — used to fold correlateDiagnoseFindings' result
// into a ladder Result's own step-sourced Verdict.LinkedFindingIDs without
// dropping either source or introducing a duplicate.
func mergeSortedUniqueStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string(nil), a...), b...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// mcpDiagLookup is the UsernameLookup the MCP diagnose runner hands the ladder.
// The MCP diagnose.run tool never escalates to capture (the one ladder rung
// that consults a username), so this is only ever a placeholder; the real
// per-invocation actor attribution for MCP lives in internal/mcp's own audit
// row (mcp:<token-name>), not the ladder's.
type mcpDiagLookup struct{}

func (mcpDiagLookup) Username(context.Context) (string, bool) { return "mcp", true }

// NewDiagnoseRunner builds the read-only diagnosis-ladder runner T-1701's MCP
// diagnose.run tool wraps. It reuses the EXACT ladder POST /diagnose runs
// (buildDiagnoseLadder), but always runs with EscalateToCapture=false — the
// capture rung is the ladder's only side effect, so an MCP-driven diagnosis is
// guaranteed free of side effects (a capture is never triggered over MCP).
// Returns nil if the simulator (inventory) isn't wired, in which case the MCP
// diagnose.run tool reports "not available" rather than mounting a broken
// runner. Advisory only: the ladder never auto-remediates.
func NewDiagnoseRunner(opts Options) func(ctx context.Context, targetRef string) (diagnose.Result, error) {
	if opts.Simulator == nil {
		return nil
	}
	ladder := buildDiagnoseLadder(opts, mcpDiagLookup{}, nil)
	return func(ctx context.Context, targetRef string) (diagnose.Result, error) {
		if _, err := inventory.ParseRef(targetRef); err != nil {
			return diagnose.Result{}, fmt.Errorf("targetRef must be a valid inventory ref (kind:node:id): %w", err)
		}
		return ladder.Run(ctx, diagnose.Request{TargetRef: targetRef, EscalateToCapture: false}), nil
	}
}
