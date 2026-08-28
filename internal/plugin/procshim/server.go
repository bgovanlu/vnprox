// SPDX-License-Identifier: Apache-2.0

package procshim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bgovanlu/vnprox/internal/plugin"
)

// Impls carries a guest plugin's concrete extension implementations — the same
// interfaces the in-process transport uses. A plugin binary fills in whichever
// points it implements and leaves the rest nil; Serve returns a per-method error
// for a call against an unimplemented point rather than panicking.
type Impls struct {
	SwitchDriver      plugin.SwitchDriver
	FlowIngestor      plugin.FlowIngestor
	FindingProducer   plugin.FindingProducer
	IngressDiscoverer plugin.IngressDiscoverer
	DashboardTiles    plugin.DashboardTileProvider
}

// Serve runs the guest-side request loop over rwc (a plugin binary's stdio),
// dispatching each framed request to the matching implementation in impls and
// writing back a framed response, until the peer closes the pipe (clean io.EOF)
// or ctx is cancelled. A dispatch error is returned to the host as a response
// Error, never a crash — a misbehaving host request cannot take the plugin down
// mid-stream.
func Serve(ctx context.Context, rwc io.ReadWriteCloser, impls Impls) error {
	defer func() { _ = rwc.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req request
		if err := readFrame(rwc, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("procshim serve: reading request: %w", err)
		}
		resp := dispatch(ctx, impls, req)
		if err := writeFrame(rwc, resp); err != nil {
			return fmt.Errorf("procshim serve: writing response: %w", err)
		}
	}
}

// dispatch routes one request to its implementation and marshals the result into
// a response. Every error path (unknown method, unimplemented point,
// implementation error, bad params) becomes a response with a non-empty Error —
// never a panic or a dropped reply, so the host's Call always unblocks.
func dispatch(ctx context.Context, impls Impls, req request) response {
	result, err := route(ctx, impls, req)
	if err != nil {
		return response{Error: err.Error()}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return response{Error: fmt.Sprintf("procshim serve: marshaling result for %q: %v", req.Method, err)}
	}
	return response{Result: raw}
}

func route(ctx context.Context, impls Impls, req request) (any, error) {
	switch req.Method {
	case methodSwitchPortConfig:
		return serveSwitchPortConfig(ctx, impls.SwitchDriver, req.Params)
	case methodSwitchSetPortConfig:
		return serveSwitchSetPortConfig(ctx, impls.SwitchDriver, req.Params)
	case methodSwitchPortNeighbor:
		return serveSwitchPortNeighbor(ctx, impls.SwitchDriver, req.Params)
	case methodSwitchClose:
		return serveSwitchClose(impls.SwitchDriver)
	case methodFlowIngest:
		return serveFlowIngest(ctx, impls.FlowIngestor, req.Params)
	case methodFindingProduce:
		return serveFindingProduce(ctx, impls.FindingProducer)
	case methodIngressDiscover:
		return serveIngressDiscover(ctx, impls.IngressDiscoverer, req.Params)
	case methodTileTiles:
		return serveTileTiles(ctx, impls.DashboardTiles)
	default:
		return nil, fmt.Errorf("procshim serve: unknown method %q", req.Method)
	}
}

func serveSwitchPortConfig(ctx context.Context, d plugin.SwitchDriver, params json.RawMessage) (any, error) {
	if d == nil {
		return nil, errUnimplemented(methodSwitchPortConfig)
	}
	var in switchPortReq
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	cfg, err := d.PortConfig(ctx, in.Port)
	if err != nil {
		return nil, err
	}
	return switchPortConfigResp{Config: cfg}, nil
}

func serveSwitchSetPortConfig(ctx context.Context, d plugin.SwitchDriver, params json.RawMessage) (any, error) {
	if d == nil {
		return nil, errUnimplemented(methodSwitchSetPortConfig)
	}
	var in switchSetPortConfigReq
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	if err := d.SetPortConfig(ctx, in.Port, in.Config); err != nil {
		return nil, err
	}
	return response{}, nil
}

func serveSwitchPortNeighbor(ctx context.Context, d plugin.SwitchDriver, params json.RawMessage) (any, error) {
	if d == nil {
		return nil, errUnimplemented(methodSwitchPortNeighbor)
	}
	var in switchPortReq
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	n, err := d.PortNeighbor(ctx, in.Port)
	if err != nil {
		return nil, err
	}
	return switchNeighborResp{Neighbor: n}, nil
}

func serveSwitchClose(d plugin.SwitchDriver) (any, error) {
	if d == nil {
		return nil, errUnimplemented(methodSwitchClose)
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return response{}, nil
}

func serveFlowIngest(ctx context.Context, ing plugin.FlowIngestor, params json.RawMessage) (any, error) {
	if ing == nil {
		return nil, errUnimplemented(methodFlowIngest)
	}
	var in flowIngestReq
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	recs, err := ing.Ingest(ctx, in.Node, in.Src, in.Payload)
	if err != nil {
		return nil, err
	}
	return flowIngestResp{Records: recs}, nil
}

func serveFindingProduce(ctx context.Context, p plugin.FindingProducer) (any, error) {
	if p == nil {
		return nil, errUnimplemented(methodFindingProduce)
	}
	fs, err := p.Produce(ctx)
	if err != nil {
		return nil, err
	}
	return findingProduceResp{Findings: fs}, nil
}

func serveIngressDiscover(ctx context.Context, d plugin.IngressDiscoverer, params json.RawMessage) (any, error) {
	if d == nil {
		return nil, errUnimplemented(methodIngressDiscover)
	}
	var in ingressDiscoverReq
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, err
	}
	st, err := d.Discover(ctx, in.Target)
	if err != nil {
		return nil, err
	}
	return ingressDiscoverResp{State: st}, nil
}

func serveTileTiles(ctx context.Context, p plugin.DashboardTileProvider) (any, error) {
	if p == nil {
		return nil, errUnimplemented(methodTileTiles)
	}
	tiles, err := p.Tiles(ctx)
	if err != nil {
		return nil, err
	}
	return tileTilesResp{Tiles: tiles}, nil
}

func errUnimplemented(method string) error {
	return fmt.Errorf("procshim serve: plugin does not implement %q", method)
}
