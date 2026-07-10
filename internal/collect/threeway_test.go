package collect_test

// F-12 collect-level test: with the interfaces(5) file actually ingested
// (hostPollOnce -> inventory.FromInterfaces), the local node's entities are
// reconciled from all three sources — interfaces file + netlink + PVE — and
// SourceHostInterfaces wins every declared field it reports, with
// provenance tagged accordingly and the raw sources retained per source.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func TestThreeWayReconciliation(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()
	go func() { _ = c.RunLLDPLoop(ctx) }()

	// pve1 is the fixture's local node: the only node whose entities can
	// carry all three sources. Wait until its bridge shows the interfaces
	// file as a contributor, not just PVE+netlink.
	brRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	waitFor(t, 3*time.Second, "vmbr0 to carry a host-interfaces contribution", func() bool {
		_, ok := graph.Snapshot().RawSource(brRef)[inventory.SourceHostInterfaces]
		return ok
	})

	snap := graph.Snapshot()
	prov, ok := snap.Provenance(brRef)
	if !ok {
		t.Fatalf("no provenance for %s", brRef)
	}

	// Declared fields: host-interfaces outranks pve-network wherever the
	// file reports the field, and the clean fixture must produce zero
	// conflicts (all sources agree on the same declared config).
	for _, field := range []string{"mtuDeclared", "addresses", "gateway", "comments", "declaredPortNames"} {
		fp, present := prov.Fields[field]
		if !present {
			t.Errorf("vmbr0 %s: no provenance entry", field)
			continue
		}
		if fp.Owner != inventory.SourceHostInterfaces {
			t.Errorf("vmbr0 %s owner = %s, want host-interfaces", field, fp.Owner)
		}
		if len(fp.Conflicts) != 0 {
			t.Errorf("vmbr0 %s: spurious conflicts on a clean fixture: %+v", field, fp.Conflicts)
		}
	}
	// Runtime-first fields stay with netlink.
	for _, field := range []string{"mtu", "portNames", "vlanAware", "stp"} {
		if owner := prov.Fields[field].Owner; owner != inventory.SourceHostNetlink {
			t.Errorf("vmbr0 %s owner = %s, want host-netlink", field, owner)
		}
	}
	if prov.HasConflicts() {
		t.Errorf("clean fixture produced provenance conflicts: %+v", prov.Fields)
	}

	br, _ := snap.Get(brRef)
	b := br.(*inventory.Bridge)
	if len(b.Addresses) != 1 || b.Addresses[0] != "10.10.0.11/24" {
		// The interfaces file spells this address+netmask style; the
		// adapter must canonicalize it to PVE's CIDR form.
		t.Errorf("vmbr0 Addresses = %v, want [10.10.0.11/24]", b.Addresses)
	}
	if !b.VlanAware || !b.VlanAwareSet || b.STP || !b.STPSet {
		t.Errorf("vmbr0 flagged bools = vlanAware(%v,set=%v) stp(%v,set=%v), want (true,set)/(false,set)", b.VlanAware, b.VlanAwareSet, b.STP, b.STPSet)
	}
	if b.Comments != "cluster/mgmt trunk, VLAN-aware" {
		t.Errorf("vmbr0 Comments = %q, want the fixture comment", b.Comments)
	}

	// The bond's declared fields reconcile the same way.
	bondRef := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	bondProv, _ := snap.Provenance(bondRef)
	if owner := bondProv.Fields["declaredSlaves"].Owner; owner != inventory.SourceHostInterfaces {
		t.Errorf("bond0 declaredSlaves owner = %s, want host-interfaces", owner)
	}

	// Raw sources: all three per-source texts retained for the bridge.
	raw := snap.RawSource(brRef)
	stanza, ok := raw[inventory.SourceHostInterfaces]
	if !ok || !strings.HasPrefix(stanza, "iface vmbr0 ") {
		t.Errorf("bridge interfaces raw source missing/malformed: %q", stanza)
	}
	var pveObj pve.NetworkInterface
	if err := json.Unmarshal([]byte(raw[inventory.SourcePVENetwork]), &pveObj); err != nil || pveObj.Iface != "vmbr0" {
		t.Errorf("bridge pve raw source not the vmbr0 API object (err=%v): %q", err, raw[inventory.SourcePVENetwork])
	}
	if _, ok := raw[inventory.SourceHostNetlink]; !ok {
		t.Errorf("bridge netlink raw rendering missing: %v", raw)
	}

	// Peer nodes have no host-side sources: pve2's bridge keeps exactly one
	// raw source (its PVE object) and pve-network provenance.
	peerRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"}
	peerRaw := snap.RawSource(peerRef)
	if len(peerRaw) != 1 {
		t.Errorf("peer bridge raw sources = %v, want pve-network only", peerRaw)
	}
	if _, ok := peerRaw[inventory.SourcePVENetwork]; !ok {
		t.Errorf("peer bridge missing pve-network raw source: %v", peerRaw)
	}
}
