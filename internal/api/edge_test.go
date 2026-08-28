// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/edge"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/sdn"
)

// fakeEdgeInterfacesSource stands in for ChangesetService.ReadRawInterfaces
// — one fixed interfaces-file content per node.
type fakeEdgeInterfacesSource struct {
	byNode map[string]string
	failed map[string]bool
}

func (f *fakeEdgeInterfacesSource) ReadRawInterfaces(_ context.Context, node string) (string, string, error) {
	if f.failed[node] {
		return "", "", errTestPeerUnreachable
	}
	return f.byNode[node], "deadbeef", nil
}

var errTestPeerUnreachable = &testErr{"simulated read failure"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// fakeEdgeGraph stands in for EdgeGraph — a snapshot built from a fixed
// entity list (cluster nodes plus guests, for node enumeration and the
// port-forward -> guest powered-off correlation).
type fakeEdgeGraph struct{ snap inventory.Snapshot }

func (f fakeEdgeGraph) Snapshot() inventory.Snapshot { return f.snap }

func buildEdgeTestSnapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
	})
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, VMID: 100, Node: "pve1", Name: "web1", Type: "qemu", Status: "running"},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}, VMID: 101, Node: "pve1", Name: "sshbox", Type: "qemu", Status: "stopped"},
	})
	return g.Snapshot()
}

// fakeEdgeSDNService stands in for SDNService.
type fakeEdgeSDNService struct{ tree sdn.Tree }

func (f fakeEdgeSDNService) Tree(context.Context) (sdn.Tree, error) { return f.tree, nil }

// fakeEdgeIPAMSource stands in for EdgeIPAMSource.
type fakeEdgeIPAMSource struct{ allocs map[string][]ipam.Allocation }

func (f fakeEdgeIPAMSource) AllAllocations(context.Context) (map[string][]ipam.Allocation, error) {
	return f.allocs, nil
}

// edgeFixtureInterfaces is T-1403's Edge/NAT fixture: a default route, one
// masquerade rule, two port-forwards (one targeting a running guest, one
// targeting a powered-off guest — this card's own exit-demo scenario), and
// one static route — built via the real mutate path (ifaces.MutateAll) so
// this fixture and the write path it exercises can never silently drift
// apart.
func edgeFixtureInterfaces(t *testing.T) string {
	t.Helper()
	start := "auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.10/24\n\tgateway 203.0.113.1\n\tbridge-ports eno1\n"
	f, err := host.ParseInterfaces([]byte(start))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ref := func(kind inventory.Kind, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: "pve1", ID: id}
	}
	ops := []ifaces.Op{
		ifaces.NatMasqueradeCreate{Target: ref(inventory.KindNatRule, "masq1"), Iface: "vmbr0", SourceCIDR: "192.168.1.0/24"},
		ifaces.NatPortForwardCreate{
			Target: ref(inventory.KindNatRule, "pf-web"), Iface: "vmbr0", Proto: "tcp",
			ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80,
		},
		ifaces.NatPortForwardCreate{
			Target: ref(inventory.KindNatRule, "pf-ssh"), Iface: "vmbr0", Proto: "tcp",
			ExtPort: 2222, IntIP: "192.168.1.99", IntPort: 22,
		},
		ifaces.RouteStaticCreate{
			Target: ref(inventory.KindStaticRoute, "lab-route"), Iface: "vmbr0",
			DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.5",
		},
	}
	if err := ifaces.MutateAll(f, ops, "cs-fixture"); err != nil {
		t.Fatalf("MutateAll: %v", err)
	}
	return f.Render()
}

func edgeTestRouter(t *testing.T, sdnSvc SDNService, ipamSrc EdgeIPAMSource) http.Handler {
	t.Helper()
	content := edgeFixtureInterfaces(t)
	ifacesSrc := &fakeEdgeInterfacesSource{byNode: map[string]string{"pve1": content}}
	return NewRouter(Options{
		Version:        "test",
		DistFS:         testDistFS(),
		Logger:         testLogger(),
		Auth:           fakeAuth{authenticated: true},
		EdgeInterfaces: ifacesSrc,
		EdgeGraph:      fakeEdgeGraph{snap: buildEdgeTestSnapshot()},
		EdgeIPAM:       ipamSrc,
		SDN:            sdnSvc,
	})
}

func TestHandleEdgeRoutes(t *testing.T) {
	r := edgeTestRouter(t, nil, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/edge/routes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got edge.RoutesView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.DefaultRoutes) != 1 || got.DefaultRoutes[0].Gateway != "203.0.113.1" {
		t.Errorf("DefaultRoutes = %+v, want one default route via 203.0.113.1", got.DefaultRoutes)
	}
	if len(got.StaticRoutes) != 1 || got.StaticRoutes[0].DestCIDR != "10.10.0.0/24" {
		t.Errorf("StaticRoutes = %+v, want one route to 10.10.0.0/24", got.StaticRoutes)
	}
}

func TestHandleEdgeNAT_GoldenFixture(t *testing.T) {
	sdnSvc := fakeEdgeSDNService{tree: sdn.Tree{Zones: []sdn.Zone{
		{ID: "zone1", Type: "simple", Vnets: []sdn.Vnet{
			{ID: "zone1/vnet1", Zone: "zone1", Subnets: []sdn.Subnet{
				{ID: "10.20.0.0/24", CIDR: "10.20.0.0/24", Gateway: "10.20.0.1", SNAT: true},
			}},
		}},
	}}}
	ipamSrc := fakeEdgeIPAMSource{allocs: map[string][]ipam.Allocation{
		"192.168.1.0/24": {
			{IP: "192.168.1.50", VMID: 100},
			{IP: "192.168.1.99", VMID: 101},
		},
	}}
	r := edgeTestRouter(t, sdnSvc, ipamSrc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/edge/nat", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got edge.NATView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Masquerade) != 1 || got.Masquerade[0].SourceCIDR != "192.168.1.0/24" {
		t.Errorf("Masquerade = %+v, want one rule for 192.168.1.0/24", got.Masquerade)
	}
	if len(got.SDNSimpleZoneNAT) != 1 || got.SDNSimpleZoneNAT[0].Zone != "zone1" {
		t.Errorf("SDNSimpleZoneNAT = %+v, want the one simple-zone SNAT subnet", got.SDNSimpleZoneNAT)
	}
	if len(got.PortForwards) != 2 {
		t.Fatalf("PortForwards = %+v, want 2 entries", got.PortForwards)
	}
	var sawPoweredOff bool
	for _, pf := range got.PortForwards {
		if pf.ID == "pf-ssh" {
			if !pf.TargetGuestPoweredOff {
				t.Errorf("pf-ssh (target 192.168.1.99, a stopped guest) not flagged TargetGuestPoweredOff: %+v", pf)
			}
			sawPoweredOff = true
		}
		if pf.ID == "pf-web" && pf.TargetGuestPoweredOff {
			t.Errorf("pf-web (target 192.168.1.50, a running guest) incorrectly flagged powered off: %+v", pf)
		}
	}
	if !sawPoweredOff {
		t.Fatalf("expected a port-forward flagged as targeting a powered-off guest (T-1403's exit-demo scenario): %+v", got.PortForwards)
	}
}

// TestEdgeRoutes_NoRequestBodyNoWriteCapability is T-1403 acceptance
// criterion 5's regression: /edge/* routes accept no request body (only
// GET is mounted — a POST/PUT/DELETE 405s or 404s, never reaching a
// mutate-capable handler) and require only netRead, never netWrite.
func TestEdgeRoutes_NoRequestBodyNoWriteCapability(t *testing.T) {
	r := edgeTestRouter(t, nil, nil)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, path := range []string{"/api/v1/edge/routes", "/api/v1/edge/nat"} {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code == http.StatusOK {
				t.Errorf("%s %s: status = 200, want any non-2xx (no mutation route exists here)", method, path)
			}
		}
	}
}
