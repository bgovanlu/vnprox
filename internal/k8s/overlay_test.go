// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
)

func TestBuildOverlay_Flannel(t *testing.T) {
	f, err := k8smock.LoadFixtureFile("../../testdata/k8s/cluster-flannel.yaml")
	if err != nil {
		t.Fatalf("LoadFixtureFile: %v", err)
	}
	nodes, pods, services, daemonsets := f.ToK8s()

	index := k8s.GuestIPIndex(func(ip string) (string, bool) {
		if ip == "10.10.0.11" {
			return "guest:pve1:105", true
		}
		return "", false
	})

	ov := k8s.BuildOverlay("cluster-1", nodes, pods, services, daemonsets, index)

	if ov.CNI != k8s.CNIFlannel {
		t.Errorf("CNI = %q, want flannel", ov.CNI)
	}
	if len(ov.PodCIDRs) != 2 {
		t.Fatalf("len(PodCIDRs) = %d, want 2", len(ov.PodCIDRs))
	}
	if ov.PodCIDRs[0].Node != "k8s-node-1" || ov.PodCIDRs[0].CIDR != "10.244.0.0/24" {
		t.Errorf("PodCIDRs[0] = %+v", ov.PodCIDRs[0])
	}
	if len(ov.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(ov.Services))
	}
	if len(ov.Pods) != 2 {
		t.Fatalf("len(Pods) = %d, want 2", len(ov.Pods))
	}
	if len(ov.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(ov.Nodes))
	}
	if !ov.Nodes[0].Matched || ov.Nodes[0].GuestRef != "guest:pve1:105" {
		t.Errorf("Nodes[0] = %+v, want matched to guest:pve1:105", ov.Nodes[0])
	}
	if ov.Nodes[1].Matched {
		t.Errorf("Nodes[1] = %+v, want unmatched", ov.Nodes[1])
	}
}

func TestBuildOverlay_MalformedPodCIDRSkipped(t *testing.T) {
	nodes := []k8s.Node{{
		Metadata: k8s.ObjectMeta{Name: "n1"},
		Spec:     k8s.NodeSpec{PodCIDR: "not-a-cidr"},
	}}
	ov := k8s.BuildOverlay("c1", nodes, nil, nil, nil, nil)
	if len(ov.PodCIDRs) != 0 {
		t.Errorf("PodCIDRs = %+v, want empty (malformed CIDR never surfaced)", ov.PodCIDRs)
	}
}
