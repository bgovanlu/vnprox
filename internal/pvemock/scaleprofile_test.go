// SPDX-License-Identifier: Apache-2.0

package pvemock

import "testing"

// TestNewScaleProfile_SmallShape checks the generator's structural
// invariants (guest count, node count, one SDN zone spanning every node) at
// a size small enough to eyeball, before trusting it at EnvelopeProfile
// scale.
func TestNewScaleProfile_SmallShape(t *testing.T) {
	cfg := ScaleProfileConfig{Nodes: 3, GuestsPerNode: 5, VNets: 4}
	f, err := NewScaleProfile(cfg)
	if err != nil {
		t.Fatalf("NewScaleProfile(%+v): %v", cfg, err)
	}
	if got, want := len(f.Nodes), cfg.Nodes; got != want {
		t.Errorf("len(Nodes) = %d, want %d", got, want)
	}
	if got, want := len(f.Cluster.Nodes), cfg.Nodes; got != want {
		t.Errorf("len(Cluster.Nodes) = %d, want %d", got, want)
	}
	var guests int
	for _, ns := range f.Nodes {
		guests += len(ns.Qemu) + len(ns.Lxc)
	}
	if want := cfg.Nodes * cfg.GuestsPerNode; guests != want {
		t.Errorf("total guests = %d, want %d", guests, want)
	}
	if got, want := len(f.SDN.Vnets), cfg.VNets; got != want {
		t.Errorf("len(SDN.Vnets) = %d, want %d", got, want)
	}
	if got, want := len(f.SDN.Zones), 1; got != want {
		t.Fatalf("len(SDN.Zones) = %d, want %d", got, want)
	}
	if got, want := len(f.SDN.Zones[0].Nodes), cfg.Nodes; got != want {
		t.Errorf("zone spans %d nodes, want %d (every node)", got, want)
	}
}

// TestNewScaleProfile_Validation checks the guard clauses report an error
// rather than panicking or silently building a degenerate fixture.
func TestNewScaleProfile_Validation(t *testing.T) {
	cases := []ScaleProfileConfig{
		{Nodes: 0, GuestsPerNode: 1, VNets: 1},
		{Nodes: 1, GuestsPerNode: -1, VNets: 1},
		{Nodes: 1, GuestsPerNode: 1, VNets: 0},
	}
	for _, cfg := range cases {
		if _, err := NewScaleProfile(cfg); err == nil {
			t.Errorf("NewScaleProfile(%+v): want error, got nil", cfg)
		}
	}
}

// TestNewScaleProfile_Envelope builds T-4107's documented 50-node/5,000-
// guest envelope and checks its shape and that generation itself is cheap
// (no disk I/O, no YAML round trip — the whole point of building the
// Fixture in Go rather than checking in a giant YAML file, T-4107's task
// card: "cheap enough to use in tests").
func TestNewScaleProfile_Envelope(t *testing.T) {
	f, err := NewScaleProfile(EnvelopeProfile)
	if err != nil {
		t.Fatalf("NewScaleProfile(EnvelopeProfile): %v", err)
	}
	if got, want := len(f.Nodes), 50; got != want {
		t.Errorf("len(Nodes) = %d, want %d", got, want)
	}
	var guests int
	for _, ns := range f.Nodes {
		guests += len(ns.Qemu) + len(ns.Lxc)
	}
	if guests != 5000 {
		t.Errorf("total guests = %d, want 5000", guests)
	}
	if got, want := len(f.SDN.Vnets), 100; got != want {
		t.Errorf("len(SDN.Vnets) = %d, want %d", got, want)
	}
}

// BenchmarkNewScaleProfile_Envelope reports the generator's own cost at
// envelope scale — this is what "cheap enough to use in tests" is checked
// against, not asserted from a static reading of the code.
func BenchmarkNewScaleProfile_Envelope(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := NewScaleProfile(EnvelopeProfile); err != nil {
			b.Fatalf("NewScaleProfile: %v", err)
		}
	}
}
