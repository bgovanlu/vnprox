// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"fmt"
	"reflect"

	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
)

// Result is one extension point's conformance outcome. Err is nil on pass.
type Result struct {
	Err   error
	Point plugin.ExtensionPoint
}

// Conformance runs the sample-contract checks against every non-nil
// implementation in set and returns one Result per checked extension point. The
// caller runs it for the in-process Set and again for a procshim-backed Set and
// asserts the two Result lists are identical (all nil) — that is the transport
// parity guarantee (T-1702 AC1). The checks compare against the fixed Sample*
// values, so any divergence (a transport dropping a field, mis-encoding a slice)
// surfaces as a non-nil Err.
func Conformance(ctx context.Context, set Set) []Result {
	var out []Result
	if set.SwitchDriver != nil {
		out = append(out, Result{Point: plugin.ExtSwitchDriver, Err: checkSwitchDriver(ctx, set.SwitchDriver)})
	}
	if set.FlowIngestor != nil {
		out = append(out, Result{Point: plugin.ExtFlowIngestor, Err: checkFlowIngestor(ctx, set.FlowIngestor)})
	}
	if set.FindingProducer != nil {
		out = append(out, Result{Point: plugin.ExtFindingProducer, Err: checkFindingProducer(ctx, set.FindingProducer)})
	}
	if set.IngressDiscoverer != nil {
		out = append(out, Result{Point: plugin.ExtIngressDiscoverer, Err: checkIngressDiscoverer(ctx, set.IngressDiscoverer)})
	}
	if set.DashboardTiles != nil {
		out = append(out, Result{Point: plugin.ExtDashboardTile, Err: checkDashboardTiles(ctx, set.DashboardTiles)})
	}
	return out
}

func checkSwitchDriver(ctx context.Context, d plugin.SwitchDriver) error {
	cfg, err := d.PortConfig(ctx, SamplePort)
	if err != nil {
		return fmt.Errorf("PortConfig: %w", err)
	}
	if cfg.Untagged != SampleUntaggedVID || cfg.Description != SampleDesc ||
		!reflect.DeepEqual(cfg.Tagged, SampleTaggedVIDs) {
		return fmt.Errorf("PortConfig mismatch: %+v", cfg)
	}
	n, err := d.PortNeighbor(ctx, SamplePort)
	if err != nil {
		return fmt.Errorf("PortNeighbor: %w", err)
	}
	if n.ChassisID != SampleChassisID {
		return fmt.Errorf("PortNeighbor mismatch: %+v", n)
	}
	// A write must round-trip without error, and Close must succeed.
	if err := d.SetPortConfig(ctx, SamplePort, cfg); err != nil {
		return fmt.Errorf("SetPortConfig: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("Close: %w", err)
	}
	return nil
}

func checkFlowIngestor(ctx context.Context, in plugin.FlowIngestor) error {
	recs, err := in.Ingest(ctx, SampleNode, "10.0.0.1", []byte("abcd"))
	if err != nil {
		return fmt.Errorf("Ingest: %w", err)
	}
	if len(recs) != 1 || recs[0].Node != SampleNode || recs[0].Bytes != 4 {
		return fmt.Errorf("Ingest mismatch: %+v", recs)
	}
	return nil
}

func checkFindingProducer(ctx context.Context, p plugin.FindingProducer) error {
	fs, err := p.Produce(ctx)
	if err != nil {
		return fmt.Errorf("Produce: %w", err)
	}
	if len(fs) != 1 || fs[0].ID != SampleFindingID || fs[0].Severity != "info" {
		return fmt.Errorf("Produce mismatch: %+v", fs)
	}
	return nil
}

func checkIngressDiscoverer(ctx context.Context, d plugin.IngressDiscoverer) error {
	st, err := d.Discover(ctx, ingress.Target{ID: "t1", Kind: SampleKind, Address: "http://10.0.0.9"})
	if err != nil {
		return fmt.Errorf("Discover: %w", err)
	}
	if st.TargetID != "t1" || !st.Reachable || len(st.Backends) != 1 || !st.Backends[0].Healthy {
		return fmt.Errorf("Discover mismatch: %+v", st)
	}
	return nil
}

func checkDashboardTiles(ctx context.Context, p plugin.DashboardTileProvider) error {
	tiles, err := p.Tiles(ctx)
	if err != nil {
		return fmt.Errorf("Tiles: %w", err)
	}
	if len(tiles) != 1 || tiles[0].ID != SampleTileID || tiles[0].Value != "42" {
		return fmt.Errorf("Tiles mismatch: %+v", tiles)
	}
	return nil
}
