package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/mcp"
	"github.com/bgovanlu/vnprox/internal/sim"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// setupMCP wires T-1701's read-only/stage-only MCP server from the daemon's
// already-constructed live services. Every read tool is a closure over the SAME
// service the corresponding HTTP handler uses; the changeset-staging tool goes
// through changeSvc's change engine (CreateWithOrigin/Validate/Diff) — the
// narrow mcp.ChangesetStager view has no apply/confirm/rollback method, so the
// stage-only boundary is structural, not merely wired. diagnose.run reuses the
// exact ladder POST /diagnose runs (api.NewDiagnoseRunner), never escalating to
// capture. A nil underlying service leaves its tool "not available" rather than
// failing construction.
func setupMCP(
	opts api.Options,
	changeSvc *change.Service,
	tokens *store.APITokenRepo,
	audit *store.AuditRepo,
	topoSvc *topology.Service,
	findingsEngine *findings.Engine,
	flowRepo *store.FlowSampleRepo,
	ipamSvc api.IPAMService,
	graph *inventory.Graph,
	logger *slog.Logger,
) (*mcp.Server, error) {
	deps := mcp.Deps{
		Auth:   mcpTokenAuth{repo: tokens},
		Audit:  audit,
		Logger: logger,
	}
	if changeSvc != nil {
		deps.Staging = changeSvc
		// T-2705: the SAME change service is the policy evaluator, so an
		// MCP-staged op is judged by T-2601's one implementation of what a
		// rule means — the identical evaluation POST /policies/test and the
		// validate stage run, not a second copy of it. Wired together with
		// Staging deliberately: a daemon that can stage must be able to
		// policy-check, and the staging tools fail closed if it cannot.
		deps.Policy = changeSvc
	}

	if topoSvc != nil {
		deps.Topology = func(_ context.Context, _ json.RawMessage) (any, error) {
			return topoSvc.Topology(topology.Filter{}), nil
		}
	}
	if findingsEngine != nil {
		deps.Findings = func(_ context.Context, _ json.RawMessage) (any, error) {
			return findingsEngine.Findings(), nil
		}
	}
	if flowRepo != nil {
		deps.Flows = func(ctx context.Context, args json.RawMessage) (any, error) {
			return mcpQueryFlows(ctx, flowRepo, opts.FlowClassifier, args)
		}
	}
	if ipamSvc != nil {
		deps.IPAM = func(ctx context.Context, _ json.RawMessage) (any, error) {
			return ipamSvc.Subnets(ctx)
		}
	}
	if graph != nil {
		deps.Simulate = func(_ context.Context, args json.RawMessage) (any, error) {
			return mcpSimulatePath(graph, args)
		}
	}
	if runner := api.NewDiagnoseRunner(opts); runner != nil {
		deps.Diagnose = func(ctx context.Context, args json.RawMessage) (any, error) {
			var req struct {
				TargetRef string `json:"targetRef"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &req); err != nil {
					return nil, err
				}
			}
			if req.TargetRef == "" {
				return nil, errors.New("targetRef is required")
			}
			return runner(ctx, req.TargetRef)
		}
	}

	return mcp.NewServer(deps)
}

// mcpTokenAuth adapts store.APITokenRepo to mcp.TokenAuthenticator, reusing
// T-1104's exact hash scheme (auth.HashAPIToken) — no second credential path.
type mcpTokenAuth struct {
	repo *store.APITokenRepo
}

func (a mcpTokenAuth) Authenticate(ctx context.Context, raw string) (mcp.TokenInfo, error) {
	rec, err := a.repo.GetByHash(ctx, auth.HashAPIToken(raw))
	if err != nil {
		return mcp.TokenInfo{}, err
	}
	if rec.RevokedAt.Valid {
		return mcp.TokenInfo{}, errors.New("token revoked")
	}
	var scopes []string
	if uerr := json.Unmarshal([]byte(rec.ScopesJSON), &scopes); uerr != nil {
		return mcp.TokenInfo{}, uerr
	}
	return mcp.TokenInfo{ID: rec.ID, Name: rec.Name, Scopes: scopes}, nil
}

func (a mcpTokenAuth) Live(ctx context.Context, id string) bool {
	rec, err := a.repo.Get(ctx, id)
	if err != nil {
		return false
	}
	return !rec.RevokedAt.Valid
}

// mcpQueryFlows parses the flows.query arguments into a store.FlowFilter and
// returns one page of matching flows.
// mcpQueryFlows answers the frozen `flows.query` MCP tool. Its payload is
// docs/api.md's documented flow.Record shape (api.FlowRecordJSON — the exact
// conversion GET /flows itself uses, classifier included), not
// store.FlowSample's bare Go field names verbatim (T-3204: found with no
// prior regression guard, so this was already wrong on the wire — see
// api.FlowRecordJSON's doc comment).
func mcpQueryFlows(ctx context.Context, repo *store.FlowSampleRepo, classifier *flow.Classifier, args json.RawMessage) (any, error) {
	var q struct {
		Guest  string `json:"guest"`
		Subnet string `json:"subnet"`
		Source string `json:"source"`
		VLAN   int    `json:"vlan"`
		Port   int    `json:"port"`
		Proto  int    `json:"proto"`
		Limit  int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &q); err != nil {
			return nil, err
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	filter := store.FlowFilter{Guest: q.Guest, Subnet: q.Subnet, Source: q.Source, VLAN: q.VLAN, Port: q.Port, Proto: q.Proto}
	samples, next, err := repo.Query(ctx, filter, "", limit)
	if err != nil {
		return nil, err
	}
	items := make([]any, len(samples))
	for i, s := range samples {
		items[i] = api.FlowRecordJSON(s, classifier)
	}
	return map[string]any{"items": items, "nextCursor": next}, nil
}

// mcpEndpoint is one endpoint of a simulate.path request.
type mcpEndpoint struct {
	Kind   string `json:"kind"`
	NicRef string `json:"nicRef"`
	IP     string `json:"ip"`
}

// mcpSimulatePath parses the simulate.path arguments and runs the static path
// simulator (the identical engine POST /simulate/path uses) over the live
// inventory snapshot.
func mcpSimulatePath(graph *inventory.Graph, args json.RawMessage) (any, error) {
	var req struct {
		Src    mcpEndpoint `json:"src"`
		Dst    mcpEndpoint `json:"dst"`
		Proto  string      `json:"proto"`
		Family string      `json:"family"`
		Port   int         `json:"port"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	src, err := toSimEndpoint(req.Src)
	if err != nil {
		return nil, err
	}
	dst, err := toSimEndpoint(req.Dst)
	if err != nil {
		return nil, err
	}
	res := sim.Simulate(
		sim.Input{Inventory: graph.Snapshot()},
		sim.Request{Src: src, Dst: dst, Proto: req.Proto, Port: req.Port, Family: sim.Family(req.Family)},
	)
	return res, nil
}

func toSimEndpoint(e mcpEndpoint) (sim.Endpoint, error) {
	out := sim.Endpoint{Kind: sim.EndpointKind(e.Kind), IP: e.IP}
	if e.NicRef != "" {
		ref, err := inventory.ParseRef(e.NicRef)
		if err != nil {
			return sim.Endpoint{}, err
		}
		out.NicRef = ref
	}
	return out, nil
}
