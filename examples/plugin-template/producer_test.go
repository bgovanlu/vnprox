// SPDX-License-Identifier: Apache-2.0

package plugintemplate

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/plugin/plugintest"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestProducer_Produce exercises this plugin's own extension-point contract
// directly: at least one well-formed finding, no error.
func TestProducer_Produce(t *testing.T) {
	fs, err := NewProducer().Produce(context.Background())
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	if len(fs) != 1 {
		t.Fatalf("Produce returned %d findings, want 1: %+v", len(fs), fs)
	}
	f := fs[0]
	if f.ID == "" || f.Severity == "" || f.Detail == "" {
		t.Fatalf("Produce returned an incomplete finding: %+v", f)
	}
}

// TestConformanceHarness_RunsAgainstTheSDKSample demonstrates
// internal/plugin/plugintest's conformance harness — the shared suite the
// SDK's own transport-parity test (T-1702 AC1) runs — from inside this
// template, so a plugin author can see how to wire it into their own test
// suite. It runs the harness against the SDK's fixture sample plugins
// (plugintest.SampleSet()), not against Producer: plugintest.Conformance
// checks fixed Sample* constants (SampleFindingID, "info", ...) that are the
// shared fixture's contract, not this plugin's own; Producer's own contract
// is TestProducer_Produce above. A plugin exposing more than one extension
// point, or adding a second (out-of-process) transport, is exactly where
// this harness earns its keep: build a plugintest.Set from your own
// implementations and diff two transports' plugintest.Conformance results,
// the way internal/plugin/procshim's own tests do.
func TestConformanceHarness_RunsAgainstTheSDKSample(t *testing.T) {
	for _, res := range plugintest.Conformance(context.Background(), plugintest.SampleSet()) {
		if res.Err != nil {
			t.Errorf("plugintest.Conformance: extension point %q: %v", res.Point, res.Err)
		}
	}
}

// TestRegistration_InstallsThroughTheRealRegistry proves Manifest+
// Registration actually install through plugin.Registry.Install — the exact
// validation (Registration.validate, plugin.NewScope, plugin.ValidateScope)
// a running vnproxd's composition root runs when it wires an in-process
// plugin in, and the exact path a hub-installed out-of-process plugin's
// manifest goes through too (cmd/vnproxd/hubinstall.go). This is the
// strongest available proof, short of booting a full vnproxd binary, that
// this template installs with no code changes: it exercises the real
// registry, not a stand-in.
func TestRegistration_InstallsThroughTheRealRegistry(t *testing.T) {
	repo := &fakePluginRepo{rows: map[string]store.PluginRow{}}
	reg := plugin.NewRegistry(plugin.Config{Repo: repo})

	if err := reg.Install(context.Background(), "test-operator", Registration()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	rows, err := reg.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != ManifestID {
		t.Fatalf("List after install = %+v, want one row with id %q", rows, ManifestID)
	}
	if got, want := rows[0].Capabilities, []string{"netRead"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("installed capabilities = %v, want %v", got, want)
	}

	found := reg.PluginFindings(context.Background())
	if len(found) != 1 || found[0].PluginID != ManifestID || len(found[0].Findings) != 1 {
		t.Fatalf("PluginFindings after install = %+v", found)
	}
}

// fakePluginRepo is an in-memory stand-in for *store.PluginRepo. It satisfies
// plugin.Config's unexported repo seam structurally (Go interface
// satisfaction needs no shared type name, only matching method signatures) —
// the same pattern internal/plugin's own tests use, reproduced here because
// this package cannot import an unexported type from another package's
// _test.go file.
type fakePluginRepo struct {
	rows map[string]store.PluginRow
}

func (f *fakePluginRepo) Upsert(_ context.Context, p store.PluginRow) error {
	f.rows[p.ID] = p
	return nil
}

func (f *fakePluginRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	r, ok := f.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	r.Enabled = enabled
	f.rows[id] = r
	return nil
}

func (f *fakePluginRepo) Delete(_ context.Context, id string) error {
	if _, ok := f.rows[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakePluginRepo) Get(_ context.Context, id string) (store.PluginRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return store.PluginRow{}, store.ErrNotFound
	}
	return r, nil
}

func (f *fakePluginRepo) List(_ context.Context) ([]store.PluginRow, error) {
	out := make([]store.PluginRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
