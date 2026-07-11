package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// SimulatorGraph is the subset of *inventory.Graph the path simulator route
// needs: a snapshot to run the pure sim.Engine over. Same one-method seam as
// FirewallGraph — the live *inventory.Graph satisfies it directly.
type SimulatorGraph interface {
	Snapshot() inventory.Snapshot
}

const maxSimulateBodyBytes = 1 << 16

// mountSimulateRoutes registers docs/api.md's `POST /simulate/path`,
// netRead-gated (a read-only static analysis over the inventory snapshot —
// it mutates nothing). graph nil-safe, like every other mountXRoutes.
func mountSimulateRoutes(r chi.Router, graph SimulatorGraph, auth AuthService) {
	if graph == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Post("/simulate/path", handleSimulatePath(graph))
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
