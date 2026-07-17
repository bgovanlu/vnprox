package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/probe"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/sim"
	"github.com/bgovanlu/vnprox/internal/store"
)

// SimulatorGraph is the subset of *inventory.Graph the path simulator route
// needs: a snapshot to run the pure sim.Engine over. Same one-method seam as
// FirewallGraph — the live *inventory.Graph satisfies it directly.
type SimulatorGraph interface {
	Snapshot() inventory.Snapshot
}

const maxSimulateBodyBytes = 1 << 16

// ProbeClientProvider supplies a live PVE session client bound to the
// requesting user's own ticket (mirrors PVEGatewayProvider's exact pattern,
// changesets.go — docs/architecture.md §6: PVE actions authenticate as the
// user), for POST /simulate/verify's guest-agent exec calls. cmd/vnproxd
// wires it from auth.Service.PVEClientFor; a nil provider (or one returning
// ok=false) means live probes can't run for this request.
type ProbeClientProvider interface {
	ProbeClientFor(ctx context.Context) (probe.PVEExecer, bool)
}

// simulateVerifyAuditor is the minimal audit-log seam POST /simulate/verify
// needs — mirrors lldpInstallAuditor's one-method seam (lldpinstall.go):
// *store.AuditRepo satisfies it directly.
type simulateVerifyAuditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// mountSimulateRoutes registers docs/api.md's `POST /simulate/path`
// (netRead-gated, read-only static analysis) and, when probeClients is
// non-nil and auth exposes UsernameLookup, T-802's `POST /simulate/verify`
// (same capability gate — a live guest-agent probe is still a diagnostic
// read, not a network-config mutation, but it does reach into a guest, so
// it is audited, unlike /simulate/path). graph nil-safe, like every other
// mountXRoutes; probeClients/audit nil-safe on top of that (the route
// simply isn't mounted without a live PVE-client provider — a live probe
// makes no sense without one).
func mountSimulateRoutes(r chi.Router, graph SimulatorGraph, probeClients ProbeClientProvider, audit simulateVerifyAuditor, auth AuthService) {
	if graph == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/simulate/path", handleSimulatePath(graph))
		if probeClients != nil {
			if lookup, ok := auth.(UsernameLookup); ok {
				r.Post("/simulate/verify", handleSimulateVerify(graph, probeClients, audit, lookup))
			}
		}
	})
}

// endpointSpec is docs/api.md's EndpointSpec: a guest NIC ref, an IP
// literal, or external. `kind` selects; `ref` (for guest-nic) is a
// "kind:node:id" triplet, `ip` (for ip) is a literal address.
type endpointSpec struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
	IP   string `json:"ip,omitempty"`
}

type simulateRequest struct {
	Src   endpointSpec `json:"src"`
	Dst   endpointSpec `json:"dst"`
	Proto string       `json:"proto,omitempty"`
	Port  int          `json:"port,omitempty"`
}

func (s endpointSpec) toEndpoint() (sim.Endpoint, string) {
	switch s.Kind {
	case string(sim.EndpointExternal):
		return sim.Endpoint{Kind: sim.EndpointExternal}, ""
	case string(sim.EndpointIP):
		if s.IP == "" {
			return sim.Endpoint{}, "ip endpoint requires an 'ip' field"
		}
		return sim.Endpoint{Kind: sim.EndpointIP, IP: s.IP}, ""
	case string(sim.EndpointGuestNic):
		ref, err := inventory.ParseRef(s.Ref)
		if err != nil || ref.Kind != inventory.KindGuestNic {
			return sim.Endpoint{}, "guest-nic endpoint requires a valid guest NIC ref (kind:node:id)"
		}
		return sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: ref}, ""
	default:
		return sim.Endpoint{}, "endpoint kind must be one of guest-nic, ip, external"
	}
}

// simBlockingRule is the API shape of a deny's blocking rule: the sim
// RuleRef with the matched rule rendered in the same camelCase ruleView
// (macro expansion included) the firewall routes use, for T-504's deep link.
type simBlockingRule struct {
	EnforcementPoint string   `json:"enforcementPoint"`
	RulesetRef       string   `json:"rulesetRef"`
	Origin           string   `json:"origin"`
	GroupName        string   `json:"groupName,omitempty"`
	Direction        string   `json:"direction"`
	Action           string   `json:"action"`
	Rule             ruleView `json:"rule"`
	Pos              int      `json:"pos"`
}

// simulateResponse mirrors sim.Result but swaps the blocking rule for the
// camelCase, macro-expanded shape.
type simulateResponse struct {
	BlockingRule *simBlockingRule     `json:"blockingRule,omitempty"`
	Missing      *sim.Missing         `json:"missing,omitempty"`
	Verdict      string               `json:"verdict"`
	Proto        string               `json:"proto,omitempty"`
	Src          sim.ResolvedEndpoint `json:"src"`
	Dst          sim.ResolvedEndpoint `json:"dst"`
	Hops         []sim.Hop            `json:"hops"`
	Caveats      []sim.Caveat         `json:"caveats"`
	Port         int                  `json:"port,omitempty"`
}

func toSimulateResponse(res sim.Result) simulateResponse {
	out := simulateResponse{
		Verdict: string(res.Verdict), Src: res.Src, Dst: res.Dst,
		Proto: res.Proto, Port: res.Port, Hops: res.Hops,
		Missing: res.Missing, Caveats: res.Caveats,
	}
	if res.BlockingRule != nil {
		br := res.BlockingRule
		out.BlockingRule = &simBlockingRule{
			EnforcementPoint: br.EnforcementPoint, RulesetRef: br.RulesetRef,
			Origin: br.Origin, GroupName: br.GroupName, Direction: br.Direction,
			Action: br.Action, Pos: br.Pos, Rule: toRuleView(br.Rule),
		}
	}
	return out
}

// handleSimulatePath implements `POST /simulate/path`.
func handleSimulatePath(graph SimulatorGraph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req simulateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSimulateBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		src, serr := req.Src.toEndpoint()
		if serr != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "src: "+serr)
			return
		}
		dst, derr := req.Dst.toEndpoint()
		if derr != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "dst: "+derr)
			return
		}

		res := sim.Simulate(sim.Input{Inventory: graph.Snapshot()},
			sim.Request{Src: src, Dst: dst, Proto: req.Proto, Port: req.Port})
		writeJSON(w, http.StatusOK, toSimulateResponse(res))
	}
}

// verifyObserved is docs/api.md's POST /simulate/verify `observed` field:
// `{outcome, detail?, execError?}`.
type verifyObserved struct {
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
	ExecError string `json:"execError,omitempty"`
}

// verifyResponse is docs/api.md's exact POST /simulate/verify contract:
// `{simulated, observed, diverges}`.
type verifyResponse struct {
	Observed  verifyObserved   `json:"observed"`
	Simulated simulateResponse `json:"simulated"`
	Diverges  bool             `json:"diverges"`
}

// handleSimulateVerify implements `POST /simulate/verify` (T-802): runs the
// identical src->dst/proto/port tuple through both the static path simulator
// (sim.Simulate — the "simulated" half, byte-identical logic to
// /simulate/path) and a real, explicit live probe via the source guest's
// QEMU guest agent (internal/probe.Run — the "observed" half), then reports
// whether the two disagree. src is restricted to a guest-nic (only a guest
// can host a live probe — an ip/external src has nothing to exec inside).
// Audited as probe.verify (docs/api.md's audit action vocabulary): every
// request that passes input validation produces exactly one audit_log row,
// result "ok" unless the probe itself could not be attempted/classified
// (observed.outcome == "error", in which case result is "error") — a
// malformed request never reaches the point of "an action was attempted",
// so it is not audited, matching every other route in this package.
func handleSimulateVerify(graph SimulatorGraph, probeClients ProbeClientProvider, audit simulateVerifyAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		var req simulateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSimulateBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}
		if req.Src.Kind != string(sim.EndpointGuestNic) {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "src must be a guest-nic — a live probe can only be run from inside a guest")
			return
		}
		src, serr := req.Src.toEndpoint()
		if serr != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "src: "+serr)
			return
		}
		dst, derr := req.Dst.toEndpoint()
		if derr != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "dst: "+derr)
			return
		}
		proto := strings.ToLower(req.Proto)
		if proto != probe.ProtoICMP && proto != probe.ProtoTCP {
			writeJSONError(w, http.StatusBadRequest, "validation_failed",
				fmt.Sprintf("proto must be %q or %q for a live probe", probe.ProtoICMP, probe.ProtoTCP))
			return
		}
		if proto == probe.ProtoTCP && req.Port <= 0 {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "port is required for a tcp probe")
			return
		}

		client, ok := probeClients.ProbeClientFor(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnprocessableEntity, "pve_session_required",
				"a live probe requires an active PVE session — log in again and retry")
			return
		}

		snap := graph.Snapshot()
		srcNode, srcVMID, srcErr := resolveQemuGuestNicOwner(snap, src.NicRef)
		if srcErr != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "src: "+srcErr)
			return
		}

		simRes := sim.Simulate(sim.Input{Inventory: snap}, sim.Request{Src: src, Dst: dst, Proto: req.Proto, Port: req.Port})

		var observed probe.Result
		if dstIP, dstErr := resolveProbeTargetIP(r.Context(), client, snap, req.Dst); dstErr != "" {
			observed = probe.Result{Outcome: probe.OutcomeError, ExecError: dstErr}
		} else {
			observed = probe.Run(r.Context(), client, probe.Request{
				Node: srcNode, VMID: srcVMID, DstIP: dstIP, Proto: proto, Port: req.Port,
			})
		}

		diverges := divergesFrom(simRes.Verdict, observed.Outcome)
		auditSimulateVerify(r.Context(), audit, username, src.NicRef.String(), proto, req.Port, simRes.Verdict, observed, diverges)

		writeJSON(w, http.StatusOK, verifyResponse{
			Simulated: toSimulateResponse(simRes),
			Observed:  verifyObserved{Outcome: string(observed.Outcome), Detail: observed.Detail, ExecError: observed.ExecError},
			Diverges:  diverges,
		})
	}
}

// divergesFrom implements docs/api.md's POST /simulate/verify divergence
// semantics: true iff the simulated verdict makes a confident reachability
// claim (allow/deny/unreachable) that disagrees with the observed outcome.
// An indeterminate simulated verdict or an "error" observed outcome never
// diverges — neither one makes a claim to contradict.
func divergesFrom(verdict sim.Verdict, outcome probe.Outcome) bool {
	if outcome == probe.OutcomeError {
		return false
	}
	switch verdict {
	case sim.VerdictAllow:
		return outcome != probe.OutcomeReachable
	case sim.VerdictDeny, sim.VerdictUnreachable:
		return outcome == probe.OutcomeReachable
	default: // sim.VerdictIndeterminate
		return false
	}
}

// resolveQemuGuestNicOwner resolves ref to its owning guest's node/vmid,
// rejecting anything that isn't a qemu guest NIC — AgentExec is qemu-only
// (internal/pve.GetGuestAgentInterfaces' precedent; no LXC guest-agent
// equivalent), so a live probe can only be sourced from one.
func resolveQemuGuestNicOwner(snap inventory.Snapshot, ref inventory.Ref) (node string, vmid int, errMsg string) {
	ent, ok := snap.Get(ref)
	if !ok {
		return "", 0, "guest NIC not found in inventory"
	}
	nic, ok := ent.(*inventory.GuestNic)
	if !ok {
		return "", 0, "ref does not name a guest NIC"
	}
	gent, ok := snap.Get(nic.Guest)
	if !ok {
		return "", 0, "owning guest not found in inventory"
	}
	guest, ok := gent.(*inventory.Guest)
	if !ok {
		return "", 0, "owning entity is not a guest"
	}
	if guest.Type != "qemu" {
		return "", 0, fmt.Sprintf("guest-agent probes are only supported for qemu guests (this guest is %q)", guest.Type)
	}
	return guest.Node, guest.VMID, ""
}

// resolveProbeTargetIP determines the concrete IP internal/probe.Run should
// target for dst — live, never from the inventory graph (which does not
// carry guest IPs; see docs/api.md's Path simulator section). An ip
// endpoint's literal address is used directly. A guest-nic endpoint's
// address is resolved via that destination guest's own live QEMU
// guest-agent-reported interfaces (the same source T-405's
// GetGuestAgentInterfaces enrichment already reads) — this is a live
// diagnostic route, so resolving live rather than reusing the simulator's
// (deliberately unpopulated) GuestIPs side-table is the honest choice here.
// external has no concrete host to probe at all.
func resolveProbeTargetIP(ctx context.Context, client probe.PVEExecer, snap inventory.Snapshot, ep endpointSpec) (ip, errMsg string) {
	switch ep.Kind {
	case string(sim.EndpointIP):
		if ep.IP == "" {
			return "", "destination ip endpoint has no address"
		}
		return ep.IP, ""
	case string(sim.EndpointExternal):
		return "", "cannot probe an external/WAN destination directly (no concrete host to target)"
	case string(sim.EndpointGuestNic):
		ref, err := inventory.ParseRef(ep.Ref)
		if err != nil || ref.Kind != inventory.KindGuestNic {
			return "", "destination guest NIC ref is invalid"
		}
		ent, ok := snap.Get(ref)
		if !ok {
			return "", "destination guest NIC not found in inventory"
		}
		nic, ok := ent.(*inventory.GuestNic)
		if !ok {
			return "", "destination ref does not name a guest NIC"
		}
		gent, ok := snap.Get(nic.Guest)
		if !ok {
			return "", "destination guest not found in inventory"
		}
		guest, ok := gent.(*inventory.Guest)
		if !ok || guest.Type != "qemu" {
			return "", "destination guest's live IP can only be resolved for a qemu guest (via its guest agent)"
		}
		resolver, ok := client.(guestAgentInterfaceReader)
		if !ok {
			return "", "destination guest IP resolution is unavailable"
		}
		ifaces, err := resolver.GetGuestAgentInterfaces(ctx, guest.Node, guest.VMID)
		if err != nil || len(ifaces) == 0 {
			return "", fmt.Sprintf("could not resolve destination guest %s's IP via its guest agent", nic.Guest)
		}
		if addr := bestAgentIPv4(ifaces, nic.Mac); addr != "" {
			return addr, ""
		}
		return "", fmt.Sprintf("destination guest %s's guest agent reported no usable IPv4 address", nic.Guest)
	default:
		return "", fmt.Sprintf("destination endpoint kind %q is not supported for a live probe", ep.Kind)
	}
}

// guestAgentInterfaceReader is the one-method seam resolveProbeTargetIP
// needs beyond probe.PVEExecer's own exec/exec-status methods — declared
// separately (rather than folded into ProbeClientProvider's contract)
// because it isn't the probe engine's own concern, only this route's dst
// resolution. *pve.Client satisfies both this and probe.PVEExecer, so a
// live ProbeClientProvider's value always also satisfies this via a type
// assertion; a narrower test double that only implements probe.PVEExecer
// simply can't resolve a guest-nic dst (resolveProbeTargetIP degrades to
// its own "unavailable" error, still a clean 200 observed.outcome=error).
type guestAgentInterfaceReader interface {
	GetGuestAgentInterfaces(ctx context.Context, node string, vmid int) ([]pve.AgentIface, error)
}

// bestAgentIPv4 prefers the address on the interface whose hardware address
// matches nicMac (the specific NIC the caller asked about), falling back to
// the first non-loopback IPv4 address any interface reports — a guest's
// agent-reported interface naming isn't guaranteed to line up with its PVE
// NIC key (docs/features/ipam.md §1's own guest-agent caveat).
func bestAgentIPv4(ifaces []pve.AgentIface, nicMac string) string {
	var fallback string
	for _, iface := range ifaces {
		if strings.EqualFold(iface.Name, "lo") {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType != "" && addr.IPAddressType != "ipv4" {
				continue
			}
			if fallback == "" {
				fallback = addr.IPAddress
			}
			if nicMac != "" && strings.EqualFold(iface.HardwareAddr, nicMac) {
				return addr.IPAddress
			}
		}
	}
	return fallback
}

// auditSimulateVerify appends one probe.verify audit_log row per attempted
// live probe (docs/api.md's audit conventions). audit == nil (no audit repo
// wired, e.g. a bare test router) simply skips logging, the same
// degraded-mode treatment auditLLDPInstall's identical nil check gets.
func auditSimulateVerify(ctx context.Context, audit simulateVerifyAuditor, username, srcRef, proto string, port int, verdict sim.Verdict, observed probe.Result, diverges bool) {
	if audit == nil {
		return
	}
	result := "ok"
	if observed.Outcome == probe.OutcomeError {
		result = "error"
	}
	detail, _ := json.Marshal(map[string]any{
		"proto": proto, "port": port,
		"simulatedVerdict": string(verdict),
		"observedOutcome":  string(observed.Outcome),
		"diverges":         diverges,
	})
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "probe.verify", Result: result}
	entry.Target.String, entry.Target.Valid = srcRef, true
	entry.DetailJSON.String, entry.DetailJSON.Valid = string(detail), true
	_, _ = audit.Append(ctx, entry)
}
