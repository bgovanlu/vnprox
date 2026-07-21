// Package plugintest is T-1702's plugin conformance harness and its one sample
// plugin per extension point (the "fixture family" the task card calls for). The
// same table-driven Conformance suite (conformance.go) runs against both the
// in-process samples and their out-of-process (procshim) counterparts, proving
// the two transports behave identically (T-1702 AC1). The samples are
// deterministic so the parity check is an exact comparison, and they double as
// the guest-side implementations the re-exec'd subprocess serves.
package plugintest

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// Set bundles one implementation per extension point. Both the in-process
// samples (SampleSet) and a procshim host's adapters are assembled into a Set,
// so Conformance can run the identical suite over either transport.
type Set struct {
	SwitchDriver      plugin.SwitchDriver
	FlowIngestor      plugin.FlowIngestor
	FindingProducer   plugin.FindingProducer
	IngressDiscoverer plugin.IngressDiscoverer
	DashboardTiles    plugin.DashboardTileProvider
}

// Sample constants — the fixed values every sample returns, so parity is an
// exact comparison across transports.
const (
	SamplePort        = "eth1"
	SampleUntaggedVID = 10
	SampleDesc        = "sample-plugin-port"
	SampleChassisID   = "aa:bb:cc:dd:ee:ff"
	SampleNode        = "pve1"
	SampleFindingID   = "plugin.sample.finding"
	SampleTileID      = "plugin.sample.tile"
	SampleKind        = ingress.Kind("sampleproxy")
)

// SampleTaggedVIDs is the fixed trunk VID set the sample switch driver reports.
var SampleTaggedVIDs = []int{20, 30}

// SampleSet returns the canonical in-process sample implementations.
func SampleSet() Set {
	return Set{
		SwitchDriver:      NewSampleSwitchDriver(),
		FlowIngestor:      sampleFlowIngestor{},
		FindingProducer:   sampleFindingProducer{},
		IngressDiscoverer: sampleIngressDiscoverer{},
		DashboardTiles:    sampleTileProvider{},
	}
}

// SampleManifest is the manifest a full-surface sample plugin installs with —
// it declares all five extension points and the capability scope their entry
// ceilings require (netWrite covers the write-adjacent switch driver; netRead
// covers the four read-only points).
func SampleManifest(transport plugin.Transport, endpoint string) plugin.Manifest {
	return plugin.Manifest{
		ID:         "com.vnprox.sample",
		Name:       "vnprox sample plugin",
		Version:    "1.0.0",
		APIVersion: plugin.APIVersion,
		Transport:  transport,
		Endpoint:   endpoint,
		ExtensionPoints: []plugin.ExtensionPoint{
			plugin.ExtSwitchDriver, plugin.ExtFlowIngestor, plugin.ExtFindingProducer,
			plugin.ExtIngressDiscoverer, plugin.ExtDashboardTile,
		},
		Capabilities: []string{"netRead", "netWrite"},
	}
}

// SampleSwitchDriver is a deterministic in-memory SwitchDriver: it reports a
// fixed pre-image for SamplePort and records writes so a test can observe them.
type SampleSwitchDriver struct {
	written map[string]switchdrv.PortConfig
}

// NewSampleSwitchDriver constructs a SampleSwitchDriver.
func NewSampleSwitchDriver() *SampleSwitchDriver {
	return &SampleSwitchDriver{written: make(map[string]switchdrv.PortConfig)}
}

// PortConfig returns the fixed sample pre-image for SamplePort.
func (d *SampleSwitchDriver) PortConfig(_ context.Context, port string) (switchdrv.PortConfig, error) {
	if port != SamplePort {
		return switchdrv.PortConfig{}, fmt.Errorf("sample switch: unknown port %q", port)
	}
	return switchdrv.PortConfig{
		LACP:        switchdrv.LACPConfig{Mode: switchdrv.LACPActive, Rate: switchdrv.LACPRateFast},
		Description: SampleDesc,
		Tagged:      append([]int(nil), SampleTaggedVIDs...),
		Untagged:    SampleUntaggedVID,
	}, nil
}

// SetPortConfig records cfg against port.
func (d *SampleSwitchDriver) SetPortConfig(_ context.Context, port string, cfg switchdrv.PortConfig) error {
	d.written[port] = cfg
	return nil
}

// PortNeighbor returns a fixed sample neighbor for SamplePort.
func (d *SampleSwitchDriver) PortNeighbor(_ context.Context, port string) (switchdrv.Neighbor, error) {
	if port != SamplePort {
		return switchdrv.Neighbor{}, fmt.Errorf("sample switch: unknown port %q", port)
	}
	return switchdrv.Neighbor{ChassisID: SampleChassisID, PortID: port}, nil
}

// Close is a no-op for the in-memory sample.
func (d *SampleSwitchDriver) Close() error { return nil }

type sampleFlowIngestor struct{}

// Ingest turns one datagram into a single deterministic record whose Bytes is
// the payload length — enough for a parity comparison across transports.
func (sampleFlowIngestor) Ingest(_ context.Context, node, src string, payload []byte) ([]flow.Record, error) {
	if node == "" {
		node = SampleNode
	}
	return []flow.Record{{
		Node:   node,
		SrcIP:  src,
		Source: flow.Source("plugin-sample"),
		Bytes:  int64(len(payload)),
		At:     1700000000,
		Proto:  6,
	}}, nil
}

type sampleFindingProducer struct{}

func (sampleFindingProducer) Produce(_ context.Context) ([]findings.Finding, error) {
	return []findings.Finding{{
		ID:       SampleFindingID,
		Source:   findings.Source("plugin"),
		Check:    "sample-check",
		Severity: "info",
		Detail:   "sample finding from the plugin conformance fixture",
		Nodes:    []string{SampleNode},
	}}, nil
}

type sampleIngressDiscoverer struct{}

func (sampleIngressDiscoverer) Discover(_ context.Context, target ingress.Target) (ingress.ProxyState, error) {
	return ingress.ProxyState{
		TargetID:  target.ID,
		Kind:      target.Kind,
		Reachable: true,
		Backends: []ingress.Backend{{
			Route:   "sample-route",
			Address: "10.0.0.9:8080",
			Healthy: true,
		}},
	}, nil
}

type sampleTileProvider struct{}

func (sampleTileProvider) Tiles(_ context.Context) ([]plugin.Tile, error) {
	return []plugin.Tile{{
		ID:    SampleTileID,
		Title: "Sample",
		Value: "42",
		Link:  "/topology",
	}}, nil
}
