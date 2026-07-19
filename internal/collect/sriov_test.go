package collect_test

// T-1506 acceptance criterion 1: a PF/VF topology fixture produces the
// correct PhysNic/VirtualFunction inventory projection through the real
// collector pipeline (pvemock -> host.Reader -> internal/collect ->
// inventory.Graph) — a golden test, no real SR-IOV hardware involved.

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const fixtureSRIOV = "../../testdata/sriov/pf-vf-topology.yaml"

func TestGolden_SRIOV_PFVFTopology(t *testing.T) {
	srv := loadFixtureServer(t, fixtureSRIOV)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()

	eno1Ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	waitFor(t, 3*time.Second, "eno1 to converge with its 2 VFs", func() bool {
		e, ok := graph.Snapshot().Get(eno1Ref)
		if !ok {
			return false
		}
		pn, ok := e.(*inventory.PhysNic)
		return ok && len(pn.SRIOVVFs) == 2
	})

	snap := graph.Snapshot()

	// --- eno1: PF with 2 VFs, exact field projection -------------------
	eno1Ent, ok := snap.Get(eno1Ref)
	if !ok {
		t.Fatalf("missing PhysNic %s", eno1Ref)
	}
	eno1, ok := eno1Ent.(*inventory.PhysNic)
	if !ok {
		t.Fatalf("eno1 entity has wrong type %T", eno1Ent)
	}
	if eno1.Driver != "ixgbevf" {
		t.Errorf("eno1 driver = %q, want ixgbevf", eno1.Driver)
	}
	if len(eno1.SRIOVVFs) != 2 {
		t.Fatalf("eno1 SRIOVVFs = %d entries, want 2: %+v", len(eno1.SRIOVVFs), eno1.SRIOVVFs)
	}

	wantVFs := map[string]inventory.VirtualFunction{
		"eno1/vf0": {
			Ref: inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf0"},
			PF:  eno1Ref, MacAddr: "aa:bb:cc:dd:ee:00", VLAN: 100,
			SpoofCheck: true, Trust: false, PCIAddr: "0000:01:00.1",
		},
		"eno1/vf1": {
			Ref: inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf1"},
			PF:  eno1Ref, MacAddr: "aa:bb:cc:dd:ee:01", VLAN: 200,
			SpoofCheck: false, Trust: true, PCIAddr: "0000:01:00.2",
		},
	}
	for _, got := range eno1.SRIOVVFs {
		want, found := wantVFs[got.ID]
		if !found {
			t.Fatalf("unexpected VF %s among eno1.SRIOVVFs", got.ID)
		}
		// AssignedGuest is resolved live by internal/topology, never at
		// ingest time (see PhysNic.SRIOVVFs' doc comment) — zero here.
		got.AssignedGuest = inventory.Ref{}
		if got != want {
			t.Errorf("VF %s = %+v, want %+v", got.ID, got, want)
		}
	}

	// --- eno2: a plain PF with no VFs configured -----------------------
	eno2Ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno2"}
	eno2Ent, ok := snap.Get(eno2Ref)
	if !ok {
		t.Fatalf("missing PhysNic %s", eno2Ref)
	}
	eno2, ok := eno2Ent.(*inventory.PhysNic)
	if !ok {
		t.Fatalf("eno2 entity has wrong type %T", eno2Ent)
	}
	if len(eno2.SRIOVVFs) != 0 {
		t.Errorf("eno2 SRIOVVFs = %+v, want none", eno2.SRIOVVFs)
	}

	// --- guest 200's hostpci0 config is ingested verbatim ---------------
	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}
	guestEnt, ok := snap.Get(guestRef)
	if !ok {
		t.Fatalf("missing Guest %s", guestRef)
	}
	guest, ok := guestEnt.(*inventory.Guest)
	if !ok {
		t.Fatalf("guest entity has wrong type %T", guestEnt)
	}
	if got := guest.HostPCI["hostpci0"]; got != "0000:01:00.1,pcie=1" {
		t.Errorf("guest 200 hostpci0 = %q, want \"0000:01:00.1,pcie=1\"", got)
	}
}
