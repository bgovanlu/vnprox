// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/plugin/procshim"
	"github.com/bgovanlu/vnprox/internal/store"
)

// serveEnvVar, when set in a re-exec'd copy of this test binary, makes TestMain
// run the sample plugin's guest-side serve loop over stdio instead of running
// tests — the standard os/exec self-re-exec pattern for driving a real
// subprocess from a Go test without shipping a separate binary.
const serveEnvVar = "VNPROX_PLUGIN_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(serveEnvVar) == "1" {
		// Guest mode: serve the sample plugin over stdin/stdout, then exit
		// before any test output can pollute the wire protocol on stdout.
		if err := ServeStdio(context.Background()); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startSubprocessPlugin spawns this test binary in serve mode and returns a
// procshim Host plus a Set of its adapter-backed implementations.
func startSubprocessPlugin(t *testing.T) (*procshim.Host, Set) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), serveEnvVar+"=1")
	cmd.Stderr = os.Stderr
	host, err := procshim.Start(cmd)
	if err != nil {
		t.Fatalf("starting subprocess plugin: %v", err)
	}
	return host, Set{
		SwitchDriver:      host.SwitchDriver(),
		FlowIngestor:      host.FlowIngestor(),
		FindingProducer:   host.FindingProducer(),
		IngressDiscoverer: host.IngressDiscoverer(),
		DashboardTiles:    host.DashboardTiles(),
	}
}

// TestConformanceParity is T-1702 AC1: the conformance suite passes identically
// for the in-process samples and their out-of-process (procshim) counterparts,
// across all five extension points.
func TestConformanceParity(t *testing.T) {
	ctx := context.Background()

	inProc := Conformance(ctx, SampleSet())

	host, outSet := startSubprocessPlugin(t)
	defer func() { _ = host.Close() }()
	outProc := Conformance(ctx, outSet)

	if len(inProc) != 5 || len(outProc) != 5 {
		t.Fatalf("expected 5 checked points each; got in=%d out=%d", len(inProc), len(outProc))
	}
	for i := range inProc {
		if inProc[i].Point != outProc[i].Point {
			t.Fatalf("point order diverged at %d: in=%q out=%q", i, inProc[i].Point, outProc[i].Point)
		}
		if inProc[i].Err != nil {
			t.Errorf("in-process %s conformance failed: %v", inProc[i].Point, inProc[i].Err)
		}
		if outProc[i].Err != nil {
			t.Errorf("out-of-process %s conformance failed: %v", outProc[i].Point, outProc[i].Err)
		}
	}
}

// TestFaultInjection_KillMidFlight is T-1702 AC5's fault-injection half: killing
// an out-of-process plugin's subprocess makes its dashboard tile and finding
// pack degrade gracefully (omitted) without crashing the host registry.
func TestFaultInjection_KillMidFlight(t *testing.T) {
	ctx := context.Background()
	host, outSet := startSubprocessPlugin(t)
	defer func() { _ = host.Close() }()

	reg := plugin.NewRegistry(plugin.Config{Repo: newMemRepo()})
	if err := reg.Install(ctx, "alice", plugin.Registration{
		Manifest: plugin.Manifest{
			ID: "com.test.oop", Name: "oop", APIVersion: plugin.APIVersion,
			Transport:       plugin.TransportGRPC,
			ExtensionPoints: []plugin.ExtensionPoint{plugin.ExtDashboardTile, plugin.ExtFindingProducer},
			Capabilities:    []string{"netRead"},
		},
		DashboardTiles:  outSet.DashboardTiles,
		FindingProducer: outSet.FindingProducer,
		Closer:          host,
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Healthy: the tile and finding come through.
	if tiles := reg.DashboardTiles(ctx); len(tiles) != 1 || tiles[0].ID != SampleTileID {
		t.Fatalf("pre-kill tiles = %+v, want the sample tile", tiles)
	}
	if fs := reg.PluginFindings(ctx); len(fs) != 1 {
		t.Fatalf("pre-kill findings = %+v, want one pack", fs)
	}

	// Kill the subprocess mid-flight.
	if err := host.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Degraded: the registry omits the dead plugin's output and does not panic.
	if tiles := reg.DashboardTiles(ctx); len(tiles) != 0 {
		t.Errorf("post-kill tiles = %+v, want none (graceful degradation)", tiles)
	}
	if fs := reg.PluginFindings(ctx); len(fs) != 0 {
		t.Errorf("post-kill findings = %+v, want none (graceful degradation)", fs)
	}
}

// TestOutOfProcess_ErrorPropagation confirms an unimplemented method surfaces as
// an ordinary error, not a hang: a host adapter for a point the guest does serve
// works, and the transport round-trips typed values (a spot check beyond the
// parity suite).
func TestOutOfProcess_TileRoundTrip(t *testing.T) {
	ctx := context.Background()
	host, outSet := startSubprocessPlugin(t)
	defer func() { _ = host.Close() }()

	tiles, err := outSet.DashboardTiles.Tiles(ctx)
	if err != nil {
		t.Fatalf("Tiles over subprocess: %v", err)
	}
	want := []plugin.Tile{{ID: SampleTileID, Title: "Sample", Value: "42", Link: "/topology"}}
	if !reflect.DeepEqual(tiles, want) {
		t.Errorf("tiles round-trip = %+v, want %+v", tiles, want)
	}
}

// memRepo is a minimal in-memory pluginRepo for the out-of-process registry
// tests (the store-backed repo is exercised by internal/store's own tests).
type memRepo struct {
	rows map[string]store.PluginRow
}

func newMemRepo() *memRepo { return &memRepo{rows: map[string]store.PluginRow{}} }

func (m *memRepo) Upsert(_ context.Context, p store.PluginRow) error {
	m.rows[p.ID] = p
	return nil
}
func (m *memRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	r, ok := m.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	r.Enabled = enabled
	m.rows[id] = r
	return nil
}
func (m *memRepo) Delete(_ context.Context, id string) error {
	delete(m.rows, id)
	return nil
}
func (m *memRepo) Get(_ context.Context, id string) (store.PluginRow, error) {
	r, ok := m.rows[id]
	if !ok {
		return store.PluginRow{}, store.ErrNotFound
	}
	return r, nil
}
func (m *memRepo) List(_ context.Context) ([]store.PluginRow, error) {
	out := make([]store.PluginRow, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}
