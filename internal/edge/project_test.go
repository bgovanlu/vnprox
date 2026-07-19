package edge_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/edge"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// buildFixtureFile applies T-1403's nat.*/route.static.* ops against a
// minimal starting interfaces file the same way the real change engine
// would (ifaces.MutateAll), so this test exercises the exact write path
// (edgeop.go) whose output ProjectRoutes/ProjectNAT must be able to read
// back — an end-to-end write-then-read round trip, not two independently
// hand-maintained fixtures that could silently drift apart.
func buildFixtureFile(t *testing.T) string {
	t.Helper()
	start := "auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.10/24\n\tgateway 203.0.113.1\n\tbridge-ports eno1\n\tbridge-stp off\n"
	f, err := host.ParseInterfaces([]byte(start))
	if err != nil {
		t.Fatalf("ParseInterfaces: %v", err)
	}
	ref := func(kind inventory.Kind, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: "pve1", ID: id}
	}
	ops := []ifaces.Op{
		ifaces.NatMasqueradeCreate{
			Target: ref(inventory.KindNatRule, "masq1"), Iface: "vmbr0", SourceCIDR: "192.168.1.0/24",
		},
		ifaces.NatPortForwardCreate{
			Target: ref(inventory.KindNatRule, "pf-web"), Iface: "vmbr0", Proto: "tcp",
			ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80, Comment: "web",
		},
		ifaces.NatPortForwardCreate{
			Target: ref(inventory.KindNatRule, "pf-ssh"), Iface: "vmbr0", Proto: "tcp",
			ExtPort: 2222, IntIP: "192.168.1.99", IntPort: 22, Comment: "ssh to stopped vm",
		},
		ifaces.RouteStaticCreate{
			Target: ref(inventory.KindStaticRoute, "lab-route"), Iface: "vmbr0",
			DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.5", Metric: 100,
		},
	}
	if err := ifaces.MutateAll(f, ops, "cs-fixture"); err != nil {
		t.Fatalf("MutateAll: %v", err)
	}
	return f.Render()
}

func TestProjectRoutes(t *testing.T) {
	content := buildFixtureFile(t)
	got, err := edge.ProjectRoutes([]edge.NodeInterfaces{{Node: "pve1", Content: content}})
	if err != nil {
		t.Fatalf("ProjectRoutes: %v", err)
	}
	if len(got.DefaultRoutes) != 1 {
		t.Fatalf("DefaultRoutes = %+v, want 1 entry", got.DefaultRoutes)
	}
	dr := got.DefaultRoutes[0]
	if dr.Node != "pve1" || dr.Iface != "vmbr0" || dr.Gateway != "203.0.113.1" {
		t.Errorf("DefaultRoutes[0] = %+v, want {pve1 vmbr0 203.0.113.1}", dr)
	}
	if len(got.StaticRoutes) != 1 {
		t.Fatalf("StaticRoutes = %+v, want 1 entry", got.StaticRoutes)
	}
	sr := got.StaticRoutes[0]
	if sr.ID != "lab-route" || sr.Iface != "vmbr0" || sr.DestCIDR != "10.10.0.0/24" || sr.Gateway != "203.0.113.5" || sr.Metric != 100 {
		t.Errorf("StaticRoutes[0] = %+v, unexpected", sr)
	}
}

func TestProjectNAT(t *testing.T) {
	content := buildFixtureFile(t)
	lookup := func(ip string) (string, bool, bool) {
		switch ip {
		case "192.168.1.50":
			return "guest:pve1:100", false, true
		case "192.168.1.99":
			return "guest:pve1:101", true, true
		default:
			return "", false, false
		}
	}
	subnets := []edge.SDNSubnetInput{
		{Zone: "zone1", ZoneType: "simple", Vnet: "vnet1", CIDR: "10.20.0.0/24", Gateway: "10.20.0.1", SNAT: true},
		{Zone: "zone2", ZoneType: "vlan", Vnet: "vnet2", CIDR: "10.30.0.0/24", SNAT: true},    // not simple: excluded
		{Zone: "zone1", ZoneType: "simple", Vnet: "vnet3", CIDR: "10.40.0.0/24", SNAT: false}, // no snat: excluded
	}

	got, err := edge.ProjectNAT([]edge.NodeInterfaces{{Node: "pve1", Content: content}}, subnets, lookup)
	if err != nil {
		t.Fatalf("ProjectNAT: %v", err)
	}

	if len(got.Masquerade) != 1 {
		t.Fatalf("Masquerade = %+v, want 1 entry", got.Masquerade)
	}
	if m := got.Masquerade[0]; m.ID != "masq1" || m.SourceCIDR != "192.168.1.0/24" || m.Iface != "vmbr0" {
		t.Errorf("Masquerade[0] = %+v, unexpected", m)
	}

	if len(got.PortForwards) != 2 {
		t.Fatalf("PortForwards = %+v, want 2 entries", got.PortForwards)
	}
	byID := map[string]edge.PortForward{}
	for _, pf := range got.PortForwards {
		byID[pf.ID] = pf
	}
	web, ok := byID["pf-web"]
	if !ok {
		t.Fatalf("missing pf-web in %+v", got.PortForwards)
	}
	if web.ExtPort != 8080 || web.IntIP != "192.168.1.50" || web.IntPort != 80 || web.Proto != "tcp" {
		t.Errorf("pf-web = %+v, unexpected", web)
	}
	if web.TargetGuestRef != "guest:pve1:100" || web.TargetGuestPoweredOff {
		t.Errorf("pf-web guest correlation = %+v, want running guest:pve1:100", web)
	}

	ssh, ok := byID["pf-ssh"]
	if !ok {
		t.Fatalf("missing pf-ssh in %+v", got.PortForwards)
	}
	if ssh.ExtPort != 2222 || ssh.IntIP != "192.168.1.99" {
		t.Errorf("pf-ssh = %+v, unexpected", ssh)
	}
	if !ssh.TargetGuestPoweredOff || ssh.TargetGuestRef != "guest:pve1:101" {
		t.Errorf("pf-ssh guest correlation = %+v, want powered-off guest:pve1:101 — this is T-1403's exit-demo scenario", ssh)
	}

	if len(got.SDNSimpleZoneNAT) != 1 || got.SDNSimpleZoneNAT[0].Zone != "zone1" || got.SDNSimpleZoneNAT[0].Subnet != "10.20.0.0/24" {
		t.Errorf("SDNSimpleZoneNAT = %+v, want exactly the one simple+snat subnet", got.SDNSimpleZoneNAT)
	}
}

func TestProjectNAT_DeleteRoundTrip(t *testing.T) {
	content := buildFixtureFile(t)
	f, err := host.ParseInterfaces([]byte(content))
	if err != nil {
		t.Fatalf("ParseInterfaces: %v", err)
	}
	del := ifaces.NatPortForwardDelete{Target: inventory.Ref{Kind: inventory.KindNatRule, Node: "pve1", ID: "pf-ssh"}}
	if err := ifaces.Mutate(f, del, "cs-fixture-2"); err != nil {
		t.Fatalf("Mutate delete: %v", err)
	}
	got, err := edge.ProjectNAT([]edge.NodeInterfaces{{Node: "pve1", Content: f.Render()}}, nil, nil)
	if err != nil {
		t.Fatalf("ProjectNAT: %v", err)
	}
	for _, pf := range got.PortForwards {
		if pf.ID == "pf-ssh" {
			t.Fatalf("pf-ssh still present after nat.portforward.delete: %+v", got.PortForwards)
		}
	}
	if len(got.PortForwards) != 1 || got.PortForwards[0].ID != "pf-web" {
		t.Errorf("PortForwards after delete = %+v, want only pf-web", got.PortForwards)
	}
}
