package docexport_test

// Golden test for T-605 acceptance criterion 3: "Export golden test on the
// three-node fixture: Markdown structure + every documented section
// present; HTML renders standalone (no external requests — CSP-style
// check); SVG topology matches the map." Builds a real inventory.Graph
// (and *sdn.Service, *topology.Service) against internal/pvemock's
// three-node-vlan.yaml fixture via one full poll cycle — the same
// pvemock -> collect -> inventory.Graph pipeline internal/topology's own
// acceptance tests use (see internal/topology/testhelpers_test.go) —
// rather than hand-building a snapshot, so this test exercises the export
// against the exact same live-shaped data the real API route sees.

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/topology"
)

const fixtureThreeNodeVlan = "../../testdata/clusters/three-node-vlan.yaml"

func buildService(t *testing.T, fixturePath string) *docexport.Service {
	t.Helper()

	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	pveClient, err := pve.New(pve.Config{
		APIURL: ts.URL, Auth: pve.AuthTicket,
		Username: "root@pam", Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:   pveClient,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}

	topoSvc := topology.NewService(graph, nil)
	sdnSvc := sdn.NewService(pveClient)

	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &docexport.Service{
		Inventory: graph,
		SDN:       sdnSvc,
		Ports:     topoSvc,
		Topo:      topoSvc,
		Now:       func() time.Time { return fixedNow },
	}
}

// documentedSections are docs/features/blueprints.md §4's six documented
// pieces of content, each of which must appear as its own heading in both
// rendered formats.
var documentedSections = []string{
	docexport.HeadingTopology,
	docexport.HeadingInterfaces,
	docexport.HeadingVlanMatrix,
	docexport.HeadingSDN,
	docexport.HeadingFirewall,
	docexport.HeadingLLDP,
}

func TestExportGolden_ThreeNodeVlan(t *testing.T) {
	svc := buildService(t, fixtureThreeNodeVlan)
	data := svc.Build(context.Background())

	if data.GeneratedAt == 0 {
		t.Fatal("Data.GeneratedAt not stamped")
	}
	if len(data.Nodes) != 3 {
		t.Fatalf("expected 3 cluster nodes, got %d: %v", len(data.Nodes), data.Nodes)
	}

	md := docexport.Markdown(data)
	for _, section := range documentedSections {
		if !strings.Contains(md, "## "+section) {
			t.Errorf("markdown missing section heading %q", section)
		}
	}

	// Per-node interface tables: every node's bond0/vmbr0/vmbr0.20 (fixture
	// content, see three-node-vlan.yaml) must appear.
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		if !strings.Contains(md, "### "+node) {
			t.Errorf("markdown missing per-node section for %s", node)
		}
	}
	for _, iface := range []string{"eno1", "eno2", "bond0", "vmbr0", "vmbr0.20"} {
		if !strings.Contains(md, iface) {
			t.Errorf("markdown missing interface %q", iface)
		}
	}

	// VLAN matrix: the fixture's vmbr0.20 sub-interface (vid 20) and the two
	// SDN VNet tags (100, 200) must all appear as rows.
	for _, vid := range []string{"| 20 |", "| 100 |", "| 200 |"} {
		if !strings.Contains(md, vid) {
			t.Errorf("markdown VLAN matrix missing row %q", vid)
		}
	}

	// SDN inventory: the fixture's zone/vnets.
	for _, want := range []string{"vlanz", "vnet100", "vnet200", "app-tier", "db-tier"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown SDN section missing %q", want)
		}
	}

	// Firewall summary: cluster ruleset must appear with its rule count.
	if !strings.Contains(md, "cluster") {
		t.Error("markdown firewall section missing cluster scope row")
	}

	// LLDP wiring: the fixture's sw-core-01/02 neighbors.
	for _, want := range []string{"sw-core-01", "sw-core-02"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown LLDP section missing %q", want)
		}
	}

	htmlDoc := docexport.HTML(data)
	for _, section := range documentedSections {
		if !strings.Contains(htmlDoc, ">"+section+"<") {
			t.Errorf("html missing section heading %q", section)
		}
	}
	if !strings.Contains(htmlDoc, "<svg") {
		t.Error("html missing embedded topology <svg>")
	}

	// SVG topology "matches the map": every physical NIC and bridge label
	// the map itself would render must appear as an SVG text label, and one
	// column header per cluster node.
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		if !strings.Contains(htmlDoc, ">"+node+"<") {
			t.Errorf("svg missing column header for node %q", node)
		}
	}
	for _, label := range []string{"vmbr0", "bond0"} {
		if !strings.Contains(htmlDoc, label) {
			t.Errorf("svg missing entity label %q", label)
		}
	}

	// CSP-style check: no external resource references anywhere (src=/
	// href= pointing at http(s)://) — the "standalone, no external
	// requests" requirement.
	if m := externalRefRE.FindString(htmlDoc); m != "" {
		t.Errorf("html contains an external resource reference: %q", m)
	}
	if strings.Contains(htmlDoc, "<script") {
		t.Error("html must not contain any <script> tag")
	}
	if strings.Contains(htmlDoc, "<link") {
		t.Error("html must not contain any <link> tag")
	}
}

var externalRefRE = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']https?://`)

func TestExportGolden_SingleNodeDegradesGracefully(t *testing.T) {
	// A daemon with no SDN client wired (cmd/vnproxd/collect.go's degraded
	// mode) must still produce a complete export, with the SDN section
	// simply reporting no zones rather than erroring the whole build.
	svc := buildService(t, "../../testdata/clusters/single-node.yaml")
	svc.SDN = nil

	data := svc.Build(context.Background())
	md := docexport.Markdown(data)
	for _, section := range documentedSections {
		if !strings.Contains(md, "## "+section) {
			t.Errorf("markdown missing section heading %q with SDN unwired", section)
		}
	}
}
