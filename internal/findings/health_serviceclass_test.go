// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/flow"
)

// fakeFlowProvider is a minimal findings.FlowProvider stand-in.
type fakeFlowProvider struct {
	err        error
	classified []flow.Classified
}

func (f fakeFlowProvider) RecentClassified() ([]flow.Classified, error) {
	return f.classified, f.err
}

func wrongNetworkRecord(node string, class flow.ServiceClass, vlan int, wrong bool) flow.Classified {
	return flow.Classified{
		Record:       flow.Record{Node: node, VLAN: vlan},
		ServiceClass: class,
		WrongNetwork: wrong,
	}
}

// TestServiceTrafficOnWrongNetwork_Fires covers T-1504 AC3: a classified
// flow's VLAN falling outside its service's declared network fires after
// hysteresis clears.
func TestServiceTrafficOnWrongNetwork_Fires(t *testing.T) {
	prov := fakeFlowProvider{classified: []flow.Classified{
		wrongNetworkRecord("pve1", flow.ServiceClassCephPublic, 20, true),
	}}
	eng := findings.New(findings.Config{Flow: prov})

	first := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork)
	if len(first) != 0 {
		t.Fatalf("service_traffic_on_wrong_network fired on the very first observation (no debounce), got %+v", first)
	}
	found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork)
	if len(found) != 1 {
		t.Fatalf("got %d service_traffic_on_wrong_network findings after 2 cycles, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Source != findings.SourceFlow {
		t.Errorf("source = %q, want flow", f.Source)
	}
	if f.Severity != findings.SeverityWarning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
	if f.Fixable {
		t.Error("service_traffic_on_wrong_network should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("service_traffic_on_wrong_network must carry a DocsLink")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}
	if !strings.Contains(f.Detail, "ceph-public") || !strings.Contains(f.Detail, "VLAN 20") {
		t.Errorf("detail = %q, want mention of ceph-public and VLAN 20", f.Detail)
	}
	if !strings.HasPrefix(f.ID, "flow:"+findings.CheckServiceTrafficOnWrongNetwork+"|ceph-public|") {
		t.Errorf("id = %q, want a flow:service_traffic_on_wrong_network|ceph-public|... prefix", f.ID)
	}
}

// TestServiceTrafficOnWrongNetwork_OnDeclaredNetwork_NoFinding: traffic that
// matches its declared network (WrongNetwork=false) never fires, even
// repeatedly.
func TestServiceTrafficOnWrongNetwork_OnDeclaredNetwork_NoFinding(t *testing.T) {
	prov := fakeFlowProvider{classified: []flow.Classified{
		wrongNetworkRecord("pve1", flow.ServiceClassCorosync, 10, false),
	}}
	eng := findings.New(findings.Config{Flow: prov})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork); len(found) != 0 {
			t.Fatalf("cycle %d: traffic on its declared network produced a finding: %+v", i, found)
		}
	}
}

// TestServiceTrafficOnWrongNetwork_Unclassified_NoFinding: unclassified
// records are never candidates.
func TestServiceTrafficOnWrongNetwork_Unclassified_NoFinding(t *testing.T) {
	prov := fakeFlowProvider{classified: []flow.Classified{
		{Record: flow.Record{Node: "pve1", VLAN: 20}, ServiceClass: flow.ServiceClassUnclassified, WrongNetwork: false},
	}}
	eng := findings.New(findings.Config{Flow: prov})
	if found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork); len(found) != 0 {
		t.Fatalf("unclassified record produced a finding: %+v", found)
	}
}

// TestServiceTrafficOnWrongNetwork_NilProvider_NoFindings: not wired ->
// quietly absent, matching every other optional Config field.
func TestServiceTrafficOnWrongNetwork_NilProvider_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{})
	if found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork); len(found) != 0 {
		t.Fatalf("nil FlowProvider produced findings: %+v", found)
	}
}

// TestServiceTrafficOnWrongNetwork_ProviderError_NoFindings: a read error
// degrades to "no findings this cycle", never a panic/crash.
func TestServiceTrafficOnWrongNetwork_ProviderError_NoFindings(t *testing.T) {
	prov := fakeFlowProvider{err: errBoom}
	eng := findings.New(findings.Config{Flow: prov})
	if found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork); len(found) != 0 {
		t.Fatalf("provider error produced findings: %+v", found)
	}
}

// TestServiceTrafficOnWrongNetwork_ClearsWhenTrafficStops: a firing finding
// clears once the offending (class, vlan) pair stops appearing in the
// provider's batch at all.
func TestServiceTrafficOnWrongNetwork_ClearsWhenTrafficStops(t *testing.T) {
	prov := &fakeFlowProviderMutable{classified: []flow.Classified{
		wrongNetworkRecord("pve1", flow.ServiceClassMigration, 30, true),
	}}
	eng := findings.New(findings.Config{Flow: prov})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork)
	if len(found) != 1 {
		t.Fatalf("got %d findings after 2 cycles, want 1", len(found))
	}

	prov.classified = nil
	if found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork); len(found) != 0 {
		t.Fatalf("finding should clear once the pair stops appearing, got %+v", found)
	}
}

// TestServiceClassFromFindingID covers the GET /history/events serviceClass
// parse path (internal/api/history.go, T-1504): a real
// service_traffic_on_wrong_network id round-trips its serviceClass segment;
// any other check's id (or a malformed one) reports ok=false.
func TestServiceClassFromFindingID(t *testing.T) {
	prov := fakeFlowProvider{classified: []flow.Classified{
		wrongNetworkRecord("pve1", flow.ServiceClassCephCluster, 20, true),
	}}
	eng := findings.New(findings.Config{Flow: prov})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckServiceTrafficOnWrongNetwork)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want 1", len(found))
	}

	class, ok := findings.ServiceClassFromFindingID(found[0].ID)
	if !ok {
		t.Fatalf("ServiceClassFromFindingID(%q) ok=false, want true", found[0].ID)
	}
	if class != "ceph-cluster" {
		t.Errorf("ServiceClassFromFindingID(%q) = %q, want ceph-cluster", found[0].ID, class)
	}

	if _, ok := findings.ServiceClassFromFindingID("health:corosync_link_degraded|pve1|ring0"); ok {
		t.Error("ServiceClassFromFindingID matched an unrelated check's id, want ok=false")
	}
	if _, ok := findings.ServiceClassFromFindingID("flow:service_traffic_on_wrong_network|"); ok {
		t.Error("ServiceClassFromFindingID matched an empty serviceClass segment, want ok=false")
	}
}

type fakeFlowProviderMutable struct {
	classified []flow.Classified
}

func (f *fakeFlowProviderMutable) RecentClassified() ([]flow.Classified, error) {
	return f.classified, nil
}
