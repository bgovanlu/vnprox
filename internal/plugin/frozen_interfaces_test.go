package plugin_test

// T-3204: field-removal-style regression guards existed for none of the
// five frozen v1 extension-point interfaces (docs/architecture.md §13.2)
// before this file — planning/tasks/phase-18.md's T-2002-bug-01 finding
// named this gap explicitly, alongside the eight uncovered MCP tools (see
// internal/mcp, cmd/vnproxd, internal/topology, internal/findings,
// internal/ipam's own frozen_mcp_payload_test.go files for those).
//
// A Go interface has no JSON shape to golden-marshal, so the analogous
// guard here is reflection over the interface TYPE: assert its exact method
// set (name + full signature) still matches what docs/architecture.md §11
// documents, in both directions — a method silently removed/renamed breaks
// every plugin built against v1 (exactly like a removed JSON field breaks
// every MCP client that reads it), and a method silently ADDED to one of
// these interfaces is just as much a breaking change under the frozen-v1
// contract (docs/architecture.md §13's additive-only policy: "no
// field/tool/event is ever renamed or removed... a new extension point is
// additive" — widening an EXISTING one is not the same as adding a new one,
// and needs a new APIVersion, not a silent method addition here).
import (
	"reflect"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
)

// methodSignatures returns "Name(argTypes) (returnTypes)" for every method
// on interface type t, sorted by name — a compact, readable fingerprint a
// test failure can diff by eye.
func methodSignatures(t reflect.Type) []string {
	out := make([]string, t.NumMethod())
	for i := range t.NumMethod() {
		m := t.Method(i)
		out[i] = m.Name + m.Type.String()[4:] // strip the leading "func" reflect prints for a method type
	}
	sort.Strings(out)
	return out
}

func assertFrozenMethods(t *testing.T, label string, iface any, want []string) {
	t.Helper()
	got := methodSignatures(reflect.TypeOf(iface).Elem())
	if len(got) != len(want) {
		t.Errorf("%s: method set changed size — got %v, want %v", label, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: method set changed — got %v, want %v", label, got, want)
			return
		}
	}
}

func TestFlowIngestor_FrozenMethodSet(t *testing.T) {
	assertFrozenMethods(t, "FlowIngestor", (*plugin.FlowIngestor)(nil), []string{
		"Ingest(context.Context, string, string, []uint8) ([]flow.Record, error)",
	})
}

func TestFindingProducer_FrozenMethodSet(t *testing.T) {
	assertFrozenMethods(t, "FindingProducer", (*plugin.FindingProducer)(nil), []string{
		"Produce(context.Context) ([]findings.Finding, error)",
	})
}

func TestDashboardTileProvider_FrozenMethodSet(t *testing.T) {
	assertFrozenMethods(t, "DashboardTileProvider", (*plugin.DashboardTileProvider)(nil), []string{
		"Tiles(context.Context) ([]plugin.Tile, error)",
	})
}

func TestSwitchDriver_FrozenMethodSet(t *testing.T) {
	assertFrozenMethods(t, "SwitchDriver", (*switchdrv.SwitchDriver)(nil), []string{
		"Close() error",
		"PortConfig(context.Context, string) (switchdrv.PortConfig, error)",
		"PortNeighbor(context.Context, string) (switchdrv.Neighbor, error)",
		"SetPortConfig(context.Context, string, switchdrv.PortConfig) error",
	})
}

func TestIngressDiscoverer_FrozenMethodSet(t *testing.T) {
	assertFrozenMethods(t, "IngressDiscoverer", (*ingress.IngressDiscoverer)(nil), []string{
		"Discover(context.Context, ingress.Target) (ingress.ProxyState, error)",
	})
}
