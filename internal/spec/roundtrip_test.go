package spec_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// AC2: export(live) then import(spec) against the same fixture yields a
// changeset with zero ops (and nothing in notInSpec, since every managed
// entity is represented) — the reconcile identity that makes GitOps viable.
func TestRoundTrip_ZeroOps(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			g := buildFixtureGraph(t, fx.path)
			snap := g.Snapshot()

			b, err := spec.Marshal(spec.Export(snap))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			parsed, err := spec.Parse(b)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ops, notInSpec, err := spec.Import(parsed, snap)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if len(ops) != 0 {
				t.Errorf("round-trip produced %d ops, want 0:\n%s", len(ops), opsDump(ops))
			}
			if len(notInSpec) != 0 {
				t.Errorf("round-trip reported %d notInSpec entities, want 0: %v", len(notInSpec), notInSpec)
			}
		})
	}
}

// AC5: an entity removed from the spec but still present live surfaces in
// notInSpec, never as a delete op. Verified against evpn-lab: drop the whole
// SDN section from the exported spec and confirm the live SDN objects come
// back as notInSpec with no delete ops (and no ops at all — the remaining
// node network still matches live).
func TestImport_NotInSpec_NoImplicitPrune(t *testing.T) {
	g := buildFixtureGraph(t, fixtureEVPNLab)
	snap := g.Snapshot()

	full := spec.Export(snap)
	if full.SDN == nil || len(full.SDN.Zones) == 0 {
		t.Fatalf("evpn-lab fixture unexpectedly has no SDN zones to drop")
	}

	// Count live SDN entities of managed kinds — these should all report as
	// notInSpec once the SDN section is removed.
	var wantNotInSpec []inventory.Ref
	for _, e := range snap.All() {
		switch e.GetRef().Kind {
		case inventory.KindSDNZone, inventory.KindSDNVnet, inventory.KindSDNSubnet:
			wantNotInSpec = append(wantNotInSpec, e.GetRef())
		}
	}
	if len(wantNotInSpec) == 0 {
		t.Fatalf("evpn-lab fixture has no live SDN entities")
	}

	pruned := full
	pruned.SDN = nil

	ops, notInSpec, err := spec.Import(pruned, snap)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("dropping the SDN section produced %d ops (must never prune), want 0:\n%s", len(ops), opsDump(ops))
	}
	if len(notInSpec) != len(wantNotInSpec) {
		t.Fatalf("notInSpec has %d entries, want %d (%v vs %v)", len(notInSpec), len(wantNotInSpec), notInSpec, wantNotInSpec)
	}
	got := map[inventory.Ref]bool{}
	for _, r := range notInSpec {
		got[r] = true
	}
	for _, want := range wantNotInSpec {
		if !got[want] {
			t.Errorf("expected %s in notInSpec, missing", want)
		}
	}
}
