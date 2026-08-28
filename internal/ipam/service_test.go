// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"bytes"
	"context"
	"net"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// ipCompare orders two IP strings numerically (via their canonical 16-byte
// form), the same ordering the address list's Entries use.
func ipCompare(a, b string) int {
	return bytes.Compare(net.ParseIP(a).To16(), net.ParseIP(b).To16())
}

// ipInFreeRanges reports whether ip falls inside any [Start, End] FreeRange.
func ipInFreeRanges(t *testing.T, ip string, ranges []ipam.FreeRange) bool {
	t.Helper()
	for _, r := range ranges {
		if ipCompare(ip, r.Start) >= 0 && ipCompare(ip, r.End) <= 0 {
			return true
		}
	}
	return false
}

const ipamLabFixture = "../../testdata/clusters/ipam-lab.yaml"

type fakeInventory struct{ g *inventory.Graph }

func (f fakeInventory) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

// ipamLabInventory hand-builds the inventory graph entities ipam-lab.yaml's
// guests/bridges imply (docs/features/ipam.md's guest-agent and detected
// non-SDN-subnet enrichment both read from InventorySource, not PVE
// directly — see service.go's doc comments) — the same "hand-built
// snapshot" pattern internal/change's own harnesses use
// (newSnapshotFakeInventory) rather than running the full collector.
func ipamLabInventory() *fakeInventory {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online", Quorate: true, Local: true},
	})
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{}, []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "300"}, VMID: 300, Name: "web1", Type: "qemu", Node: "pve1", Status: "running"},
		&inventory.GuestNic{
			Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "300/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "300"}, Key: "net0",
			TargetName: "vmbr0", Mac: "AA:BB:CC:DD:EE:01", Vid: 10,
		},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "301"}, VMID: 301, Name: "web2", Type: "qemu", Node: "pve1", Status: "running"},
		&inventory.GuestNic{
			Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "301/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "301"}, Key: "net0",
			TargetName: "vmbr0", Mac: "AA:BB:CC:DD:EE:02", Vid: 10,
		},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "302"}, VMID: 302, Name: "web3", Type: "qemu", Node: "pve1", Status: "running"},
		&inventory.GuestNic{
			Ref:   inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "302/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "302"}, Key: "net0",
			TargetName: "vmbr0", Mac: "AA:BB:CC:DD:EE:03", Vid: 10,
		},
	})
	// Bridge.Addresses is a "declared" field (internal/inventory/merge.go's
	// ownershipRules) owned by host-interfaces (with pve-network as a
	// cross-check) — SourcePVECluster/SourcePVEGuest above is the wrong
	// source for it and silently resolves to an empty Addresses (no
	// ownership-recognized source reported it), so bridges are seeded via
	// their own ApplyPoll tagged SourceHostInterfaces.
	g.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux, Addresses: []string{"10.50.10.11/24"}},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}, Name: "vmbr1", Virt: inventory.BridgeLinux, Addresses: []string{"192.168.99.1/24"}},
	})
	return &fakeInventory{g: g}
}

func newIpamTestService(t *testing.T) *ipam.Service {
	t.Helper()
	f, err := pvemock.LoadFixture(ipamLabFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return ipam.NewService(ipam.Config{PVE: client, Inventory: ipamLabInventory()})
}

// Acceptance criterion 1 (end-to-end through the real PVE client + mock
// server + inventory graph): the ipam-lab.yaml fixture's deliberately messy
// allocation set renders with the exact per-cell states/confidence labels
// documented in that fixture's header comment, covering all four
// confidence labels.
func TestService_Allocations_GoldenCellStateMap(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	list, err := svc.Allocations(ctx, "10.50.0.0/24")
	if err != nil {
		t.Fatalf("Allocations: %v", err)
	}

	byIP := map[string]ipam.Cell{}
	for _, c := range list.Entries {
		byIP[c.IP] = c
	}

	// Every occupied address renders one Entry with the documented state and
	// confidence label. Free addresses (10.50.0.99) never appear as an
	// entry — they are covered by the collapsed FreeRanges instead.
	golden := map[string]struct {
		state ipam.CellState
		conf  ipam.Confidence
	}{
		"10.50.0.1":  {ipam.CellGateway, ipam.ConfidenceAllocated},
		"10.50.0.10": {ipam.CellAllocated, ipam.ConfidenceBoth},
		"10.50.0.11": {ipam.CellConflict, ipam.ConfidenceAllocated},
		"10.50.0.20": {ipam.CellAllocated, ipam.ConfidenceAllocated},
		"10.50.0.77": {ipam.CellConflict, ipam.ConfidenceConflict},
		"10.50.0.88": {ipam.CellObserved, ipam.ConfidenceObserved},
	}
	for ip, want := range golden {
		got, ok := byIP[ip]
		if !ok {
			t.Fatalf("%s missing from address list entries", ip)
		}
		if got.State != want.state || got.Confidence != want.conf {
			t.Errorf("%s: got (state=%s, confidence=%s), want (state=%s, confidence=%s)", ip, got.State, got.Confidence, want.state, want.conf)
		}
	}

	if _, ok := byIP["10.50.0.99"]; ok {
		t.Error("free address 10.50.0.99 must not appear as an entry — it belongs in a FreeRange")
	}
	if !ipInFreeRanges(t, "10.50.0.99", list.FreeRanges) {
		t.Error("free address 10.50.0.99 is not covered by any FreeRange")
	}

	// Entries are sorted ascending by numeric address.
	for i := 1; i < len(list.Entries); i++ {
		if ipCompare(list.Entries[i-1].IP, list.Entries[i].IP) >= 0 {
			t.Fatalf("entries not sorted: %s !< %s", list.Entries[i-1].IP, list.Entries[i].IP)
		}
	}

	// The state buckets plus the free ranges partition the /24's 254 usable
	// hosts exactly, so the summary strip's segments always add up.
	occupied := len(list.Entries)
	if got := list.Counts.Free + occupied; got != 254 {
		t.Errorf("free (%d) + occupied (%d) = %d, want 254 usable hosts", list.Counts.Free, occupied, got)
	}

	seen := map[ipam.Confidence]bool{}
	for _, c := range list.Entries {
		if c.Confidence != "" {
			seen[c.Confidence] = true
		}
	}
	for _, want := range []ipam.Confidence{ipam.ConfidenceAllocated, ipam.ConfidenceObserved, ipam.ConfidenceBoth, ipam.ConfidenceConflict} {
		if !seen[want] {
			t.Errorf("confidence label %q never appears in the live-rendered list", want)
		}
	}

	// Acceptance criterion 2: all three conflict types, each with a
	// suggested resolution, on this brownfield fixture.
	gotTypes := map[string]bool{}
	for _, c := range list.Conflicts {
		gotTypes[c.Type] = true
		if c.Suggestion == "" {
			t.Errorf("conflict %+v has no suggested resolution", c)
		}
	}
	for _, want := range []string{"duplicate_ip", "allocated_dark", "observed_unallocated"} {
		if !gotTypes[want] {
			t.Errorf("conflict type %q not detected end-to-end", want)
		}
	}
}

func TestService_Subnets_ListsSDNAndNonSDNSubnets(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	resp, err := svc.Subnets(ctx)
	if err != nil {
		t.Fatalf("Subnets: %v", err)
	}
	byCIDR := map[string]ipam.Subnet{}
	for _, s := range resp.Items {
		byCIDR[s.CIDR] = s
	}

	sdnRow, ok := byCIDR["10.50.0.0/24"]
	if !ok {
		t.Fatal("SDN subnet 10.50.0.0/24 missing from GET /ipam/subnets")
	}
	if sdnRow.Source != "sdn" || sdnRow.ReadOnly {
		t.Errorf("SDN subnet row = %+v, want source=sdn, readOnly=false", sdnRow)
	}
	if sdnRow.Zone != "labz" || sdnRow.Vnet != "vnet10" {
		t.Errorf("SDN subnet zone/vnet = %q/%q, want labz/vnet10", sdnRow.Zone, sdnRow.Vnet)
	}
	if sdnRow.Conflicts == 0 {
		t.Error("expected the brownfield SDN subnet to report conflicts > 0")
	}

	mgmt, ok := byCIDR["10.50.10.0/24"]
	if !ok {
		t.Fatal("detected non-SDN management subnet 10.50.10.0/24 missing")
	}
	if mgmt.Source != "bridge" || !mgmt.ReadOnly {
		t.Errorf("bridge-derived subnet row = %+v, want source=bridge, readOnly=true", mgmt)
	}

	legacy, ok := byCIDR["192.168.99.0/24"]
	if !ok {
		t.Fatal("detected non-SDN legacy subnet 192.168.99.0/24 missing")
	}
	if legacy.Source != "bridge" || !legacy.ReadOnly {
		t.Errorf("legacy subnet row = %+v, want source=bridge, readOnly=true", legacy)
	}
}

func TestService_Allocations_UnknownSubnet(t *testing.T) {
	svc := newIpamTestService(t)
	if _, err := svc.Allocations(context.Background(), "203.0.113.0/24"); err != ipam.ErrSubnetNotFound {
		t.Fatalf("err = %v, want ErrSubnetNotFound", err)
	}
}

func TestService_AllocationsCSV_OnlyNonFreeRows(t *testing.T) {
	svc := newIpamTestService(t)
	data, err := svc.AllocationsCSV(context.Background(), "10.50.0.0/24")
	if err != nil {
		t.Fatalf("AllocationsCSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	sort.Strings(lines)
	// header + gateway + 10 + 11 + 20 + 77(x1, one row per IP not per
	// source) + 88 — assert the header plus at least the six
	// deliberately-scripted rows, and that "free" addresses (e.g.
	// 10.50.0.99) never appear.
	if len(lines) < 7 {
		t.Fatalf("expected header + >=6 data rows, got %d lines: %q", len(lines), lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "10.50.0.99") {
			t.Errorf("CSV export must not include free addresses: %q", l)
		}
	}
}
