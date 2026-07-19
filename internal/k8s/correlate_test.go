package k8s_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
)

// TestCorrelateNodes_MatchedAndUnmatched is T-1501 AC2: a fixture where a
// k8s node's InternalIP matches a known IPAM-allocated guest address
// resolves the correct guest Ref; an unmatched node returns "unmatched",
// never a wrong Ref.
func TestCorrelateNodes_MatchedAndUnmatched(t *testing.T) {
	nodes := []k8s.Node{
		{
			Metadata: k8s.ObjectMeta{Name: "k8s-node-1"},
			Status:   k8s.NodeStatus{Addresses: []k8s.NodeAddress{{Type: "InternalIP", Address: "10.10.0.11"}}},
		},
		{
			Metadata: k8s.ObjectMeta{Name: "k8s-node-2"},
			Status:   k8s.NodeStatus{Addresses: []k8s.NodeAddress{{Type: "InternalIP", Address: "10.10.0.99"}}},
		},
		{
			// No InternalIP reported at all.
			Metadata: k8s.ObjectMeta{Name: "k8s-node-3"},
		},
	}

	index := k8s.GuestIPIndex(func(ip string) (string, bool) {
		if ip == "10.10.0.11" {
			return "guest:pve1:105", true
		}
		return "", false
	})

	got := k8s.CorrelateNodes(nodes, index)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	if !got[0].Matched || got[0].GuestRef != "guest:pve1:105" {
		t.Errorf("node1 correlation = %+v, want matched guest:pve1:105", got[0])
	}
	if got[1].Matched || got[1].GuestRef != "" {
		t.Errorf("node2 correlation = %+v, want unmatched with empty ref", got[1])
	}
	if got[2].Matched || got[2].GuestRef != "" || got[2].InternalIP != "" {
		t.Errorf("node3 correlation = %+v, want unmatched, no internal IP", got[2])
	}
}

func TestCorrelateNodes_NilIndexLeavesEveryNodeUnmatched(t *testing.T) {
	nodes := []k8s.Node{{
		Metadata: k8s.ObjectMeta{Name: "n1"},
		Status:   k8s.NodeStatus{Addresses: []k8s.NodeAddress{{Type: "InternalIP", Address: "10.0.0.1"}}},
	}}
	got := k8s.CorrelateNodes(nodes, nil)
	if len(got) != 1 || got[0].Matched {
		t.Errorf("got = %+v, want one unmatched entry", got)
	}
}
