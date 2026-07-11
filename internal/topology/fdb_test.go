package topology_test

// T-306 MAC/FDB browser acceptance tests. TestFDB_OwnershipLabels_Guest,
// TestFDB_Stale and TestFDBDetail_OwnerLabels run the full
// pvemock -> collect -> inventory.Graph pipeline against the
// three-node-vlan fixture's testdata/clusters/three-node-vlan.yaml fdb:
// entries (see that file's T-306 comments), but that harness only polls
// host-netlink state for the daemon's own local node (pve1) — no
// internal/peer wiring — so cluster-wide behavior (a MAC search spanning
// pve1/pve2/pve3) is exercised instead against a hand-built multi-node
// inventory.Graph in TestFDB_ClusterWide/TestFDBSearch_RankedClusterWide,
// the same "ApplyPoll entities onto several nodes directly" pattern
// ports_test.go's TestPorts_Table already uses for the same reason: T-306's
// job is the merge/label/search logic over whatever the graph holds, not
// re-proving T-303's peer fan-out actually populates it (that's T-303's own
// test suite's job — see internal/collect/clusterharness_test.go).

import (
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestFDB_OwnershipLabels_Guest is half of acceptance criterion #1: "guest
// MAC → its bridge+port+guest link; unknown MAC on an uplink port →
// labeled unknown with port shown."
func TestFDB_OwnershipLabels_Guest(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	rows := topology.FDB(graph.Snapshot())
	if len(rows) == 0 {
		t.Fatalf("FDB() returned no rows")
	}

	byMac := map[string]topology.FDBRow{}
	for _, r := range rows {
		byMac[r.Node+"/"+r.Mac] = r
	}

	guestRow, ok := byMac["pve1/BC:24:11:AA:02:C8"]
	if !ok {
		t.Fatalf("guest MAC row not found; rows=%+v", rows)
	}
	if guestRow.Bridge != "vmbr0" {
		t.Errorf("guest row Bridge = %q, want vmbr0", guestRow.Bridge)
	}
	if guestRow.Port != "tap200i0" {
		t.Errorf("guest row Port = %q, want tap200i0", guestRow.Port)
	}
	if guestRow.Owner != topology.OwnerGuest {
		t.Errorf("guest row Owner = %q, want %q", guestRow.Owner, topology.OwnerGuest)
	}
	if guestRow.OwnerRef != "guest:pve1:200" {
		t.Errorf("guest row OwnerRef = %q, want guest:pve1:200 (deep link)", guestRow.OwnerRef)
	}
	if guestRow.OwnerLabel != "app01" {
		t.Errorf("guest row OwnerLabel = %q, want app01", guestRow.OwnerLabel)
	}

	unknownRow, ok := byMac["pve1/DE:AD:BE:EF:00:01"]
	if !ok {
		t.Fatalf("unknown-uplink MAC row not found; rows=%+v", rows)
	}
	if unknownRow.Owner != topology.OwnerUnknown {
		t.Errorf("unknown row Owner = %q, want %q", unknownRow.Owner, topology.OwnerUnknown)
	}
	if unknownRow.Port != "bond0" {
		t.Errorf("unknown row Port = %q, want bond0 (uplink)", unknownRow.Port)
	}
	if unknownRow.OwnerRef != "" {
		t.Errorf("unknown row OwnerRef = %q, want empty", unknownRow.OwnerRef)
	}
}

// TestFDB_Stale is acceptance criterion #3: "Stale FDB entries
// (fixture-aged) marked per staleness rules."
func TestFDB_Stale(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	rows := topology.FDB(graph.Snapshot())

	var stale, fresh int
	for _, r := range rows {
		if r.Node != "pve1" || r.Bridge != "vmbr0" {
			continue
		}
		switch r.Mac {
		case "CA:FE:BA:BE:00:02":
			if !r.Stale {
				t.Errorf("CA:FE:BA:BE:00:02 Stale = false, want true (fixture declares stale: true)")
			}
			stale++
		case "DE:AD:BE:EF:00:01", "BC:24:11:AA:02:C8":
			if r.Stale {
				t.Errorf("%s Stale = true, want false", r.Mac)
			}
			fresh++
		}
	}
	if stale != 1 || fresh != 2 {
		t.Fatalf("stale=%d fresh=%d, want 1 and 2 (missing expected rows)", stale, fresh)
	}
}

// TestFDBDetail_OwnerLabels is the inspector-integration deliverable:
// GET /inventory/{ref} for a bridge shows its FDB with owner labels baked
// in, not just the raw Mac/Port/Vlan tuples.
func TestFDBDetail_OwnerLabels(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	snap := graph.Snapshot()

	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	detail, ok := topology.Detail(snap, ref)
	if !ok {
		t.Fatalf("Detail(bridge:pve1:vmbr0) not found")
	}
	raw, ok := detail.Fields["FDB"]
	if !ok {
		t.Fatalf("bridge detail Fields has no FDB key; fields=%+v", detail.Fields)
	}
	rows, ok := raw.([]topology.FDBRow)
	if !ok {
		t.Fatalf("bridge detail Fields[FDB] has type %T, want []topology.FDBRow", raw)
	}
	found := false
	for _, r := range rows {
		if r.Mac == "BC:24:11:AA:02:C8" {
			found = true
			if r.Owner != topology.OwnerGuest || r.OwnerRef != "guest:pve1:200" {
				t.Errorf("inspector FDB row = %+v, want owner=guest ownerRef=guest:pve1:200", r)
			}
		}
	}
	if !found {
		t.Errorf("guest MAC not found in bridge inspector's FDB rows: %+v", rows)
	}
}

// clusterWideSnapshot hand-builds a 3-node inventory.Graph (pve1/pve2/pve3),
// each with its own bridge + FDB table, a PhysNic (for the vnprox-known
// label), and one guest NIC (for the guest label) — the multi-node
// equivalent of what a real T-303 peer fan-out would have merged in, built
// directly so this package's own tests don't need to stand up
// internal/peer/internal/collect's full multi-daemon harness.
func clusterWideSnapshot(t *testing.T) inventory.Snapshot {
	t.Helper()
	g := inventory.NewGraph()

	guest := &inventory.Guest{
		Ref:  inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"},
		VMID: 200, Name: "app01", Type: "qemu", Node: "pve1", Status: "running",
	}
	guestNic := &inventory.GuestNic{
		Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "200/net0"},
		Guest: guest.Ref, Key: "net0", Mac: "BC:24:11:AA:02:C8",
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{}, []inventory.Entity{guest, guestNic})

	bridges := map[string][]inventory.FDBEntry{
		// pve1 also carries the first uplink unknown so all three nodes
		// share the "DE:AD:BE:EF:00:0*" family for the ranked cluster-wide
		// search assertion below.
		"pve1": {
			{Mac: "BC:24:11:AA:02:C8", Port: "tap200i0", Vlan: 100},
			{Mac: "DE:AD:BE:EF:00:01", Port: "bond0", Vlan: 1},
		},
		"pve2": {
			{Mac: "BC:24:11:01:00:01", Port: "bond0", Vlan: 1},
			{Mac: "DE:AD:BE:EF:00:02", Port: "bond0", Vlan: 1},
		},
		"pve3": {{Mac: "DE:AD:BE:EF:00:03", Port: "bond0", Vlan: 1}},
	}
	for node, fdb := range bridges {
		br := &inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: "vmbr0"},
			Name: "vmbr0", Virt: inventory.BridgeLinux, FDB: fdb,
		}
		// A node's PhysNic and Bridge are both host-netlink-sourced, same
		// scope: they must land in a single ApplyPoll call, since
		// ApplyPoll reconciles (drops any previously-contributed Ref this
		// poll's list omits) per (source, scope) pair — two separate calls
		// for the same node would have the second wipe the first's PhysNic.
		entities := []inventory.Entity{br}
		if node == "pve1" {
			entities = append(entities, &inventory.PhysNic{
				Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: "eno1"},
				Name: "eno1", Mac: "BC:24:11:01:00:01",
			})
		}
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, entities)
	}
	return g.Snapshot()
}

// TestFDB_VnproxKnownLabel is the other half of acceptance criterion #1:
// a MAC that matches a known infra PhysNic elsewhere in the cluster (not a
// guest) is labeled vnprox-known, with a deep-link to that NIC.
func TestFDB_VnproxKnownLabel(t *testing.T) {
	rows := topology.FDB(clusterWideSnapshot(t))
	var found bool
	for _, r := range rows {
		if r.Node == "pve2" && r.Mac == "BC:24:11:01:00:01" {
			found = true
			if r.Owner != topology.OwnerVnproxKnown {
				t.Errorf("Owner = %q, want %q", r.Owner, topology.OwnerVnproxKnown)
			}
			if r.OwnerRef != "physnic:pve1:eno1" {
				t.Errorf("OwnerRef = %q, want physnic:pve1:eno1", r.OwnerRef)
			}
		}
	}
	if !found {
		t.Fatalf("pve2's vnprox-known row not found; rows=%+v", rows)
	}
}

// TestFDBSearch_RankedClusterWide is acceptance criterion #2: "Partial-MAC
// search returns ranked matches cluster-wide."
func TestFDBSearch_RankedClusterWide(t *testing.T) {
	snap := clusterWideSnapshot(t)

	// "deadbeef00" (separators stripped) matches the three
	// DE:AD:BE:EF:00:0{1,2,3} entries planted one per node — verifies the
	// search spans the whole cluster, not just one node's bridges.
	results := topology.FDBSearch(snap, "deadbeef00")
	if len(results) != 3 {
		t.Fatalf("FDBSearch(deadbeef00) = %d results, want 3; got %+v", len(results), results)
	}
	nodes := map[string]bool{}
	for _, r := range results {
		nodes[r.Node] = true
		if r.Score == 0 {
			t.Errorf("result %+v has zero score", r)
		}
	}
	for _, want := range []string{"pve1", "pve2", "pve3"} {
		if !nodes[want] {
			t.Errorf("FDBSearch(deadbeef00) missing a hit on %s; got %+v", want, results)
		}
	}

	// An exact full-MAC query must outrank a mere substring/prefix match:
	// query the guest's MAC exactly and confirm it's the top (and only
	// max-score) hit.
	exact := topology.FDBSearch(snap, "BC:24:11:AA:02:C8")
	if len(exact) == 0 {
		t.Fatalf("FDBSearch(exact guest mac) returned no results")
	}
	sort.SliceStable(exact, func(i, j int) bool { return exact[i].Score > exact[j].Score })
	if exact[0].Mac != "BC:24:11:AA:02:C8" || exact[0].Owner != topology.OwnerGuest {
		t.Errorf("top exact-match result = %+v, want the guest MAC row first", exact[0])
	}

	if got := topology.FDBSearch(snap, "   "); got != nil {
		t.Errorf("FDBSearch(blank) = %+v, want nil (nothing typed yet)", got)
	}
}
