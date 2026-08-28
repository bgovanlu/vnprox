// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/k8s"
)

func nodePortService() k8s.Service {
	return k8s.Service{
		Metadata: k8s.ObjectMeta{Namespace: "default", Name: "web"},
		Spec: k8s.ServiceSpec{
			Type: "NodePort",
			Ports: []k8s.ServicePort{
				{Port: 80, NodePort: 30080, Protocol: "TCP"},
			},
		},
	}
}

func matchedNode(ref string) k8s.NodeCorrelation {
	return k8s.NodeCorrelation{K8sNode: "k8s-node-1", InternalIP: "10.10.0.11", GuestRef: ref, Matched: true}
}

// TestCheckNodePortExposure_FiresWithNoCoveringRule is T-1501 AC3's
// "fires" direction: no firewall ruleset at all on the backing guest.
func TestCheckNodePortExposure_FiresWithNoCoveringRule(t *testing.T) {
	nodes := []k8s.NodeCorrelation{matchedNode("guest:pve1:105")}
	lookup := k8s.FwLookup(func(guestRef string) (guest, cluster *inventory.FwRuleset) {
		return nil, nil
	})

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{nodePortService()}, nodes, lookup)
	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Namespace != "default" || f.Service != "web" || f.NodePort != 30080 || f.Proto != "tcp" {
		t.Errorf("finding = %+v", f)
	}
	if len(f.Refs) != 1 || f.Refs[0] != "guest:pve1:105" {
		t.Errorf("finding.Refs = %v", f.Refs)
	}
}

// TestCheckNodePortExposure_SilentWithCoveringRule is T-1501 AC3's
// "silent" direction: an explicit enabled inbound ACCEPT rule on the
// backing guest's own ruleset covers the exact NodePort.
func TestCheckNodePortExposure_SilentWithCoveringRule(t *testing.T) {
	nodes := []k8s.NodeCorrelation{matchedNode("guest:pve1:105")}
	guestRuleset := &inventory.FwRuleset{
		Enabled: true,
		Rules: []inventory.FwRule{
			{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "30080"},
		},
	}
	lookup := k8s.FwLookup(func(guestRef string) (guest, cluster *inventory.FwRuleset) {
		return guestRuleset, nil
	})

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{nodePortService()}, nodes, lookup)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestCheckNodePortExposure_ClusterScopeRuleAlsoCovers(t *testing.T) {
	nodes := []k8s.NodeCorrelation{matchedNode("guest:pve1:105")}
	clusterRuleset := &inventory.FwRuleset{
		Enabled: true,
		Rules: []inventory.FwRule{
			{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "30000:31000"},
		},
	}
	lookup := k8s.FwLookup(func(guestRef string) (guest, cluster *inventory.FwRuleset) {
		return nil, clusterRuleset
	})

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{nodePortService()}, nodes, lookup)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none (cluster-scope rule should cover)", findings)
	}
}

func TestCheckNodePortExposure_ClusterIPServiceNeverFires(t *testing.T) {
	svc := k8s.Service{
		Metadata: k8s.ObjectMeta{Namespace: "default", Name: "internal"},
		Spec:     k8s.ServiceSpec{Type: "ClusterIP", Ports: []k8s.ServicePort{{Port: 443}}},
	}
	nodes := []k8s.NodeCorrelation{matchedNode("guest:pve1:105")}
	lookup := k8s.FwLookup(func(string) (guest, cluster *inventory.FwRuleset) { return nil, nil })

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{svc}, nodes, lookup)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none (ClusterIP has no NodePort)", findings)
	}
}

func TestCheckNodePortExposure_NoMatchedNodesNeverFires(t *testing.T) {
	nodes := []k8s.NodeCorrelation{{K8sNode: "n1", Matched: false}}
	lookup := k8s.FwLookup(func(string) (guest, cluster *inventory.FwRuleset) { return nil, nil })

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{nodePortService()}, nodes, lookup)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none (no matched node to evaluate)", findings)
	}
}

func TestCheckNodePortExposure_DisabledRulesetNeverCovers(t *testing.T) {
	nodes := []k8s.NodeCorrelation{matchedNode("guest:pve1:105")}
	guestRuleset := &inventory.FwRuleset{
		Enabled: false, // firewall disabled at the scope level
		Rules: []inventory.FwRule{
			{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "30080"},
		},
	}
	lookup := k8s.FwLookup(func(string) (guest, cluster *inventory.FwRuleset) { return guestRuleset, nil })

	findings := k8s.CheckNodePortExposure("c1", []k8s.Service{nodePortService()}, nodes, lookup)
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want 1 (disabled ruleset provides no coverage)", findings)
	}
}
