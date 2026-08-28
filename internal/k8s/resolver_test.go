// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
)

func testOverlay() k8s.Overlay {
	return k8s.Overlay{
		ClusterID: "c1",
		PodCIDRs:  []k8s.PodCIDR{{Node: "k8s-node-1", CIDR: "10.244.0.0/24"}},
		Services: []k8s.ServiceInfo{
			{Namespace: "default", Name: "web", ClusterIP: "10.96.0.10", Type: "NodePort"},
		},
		Pods: []k8s.PodSummary{
			{Namespace: "default", Name: "web-abc", PodIP: "10.244.0.5"},
		},
	}
}

func TestK8sResolver_ExactServiceMatch(t *testing.T) {
	r := k8s.NewK8sResolver()
	r.Refresh(testOverlay())

	ref, ok := r.Resolve("10.96.0.10")
	if !ok || ref != k8s.ServiceRef("c1", "default", "web") {
		t.Errorf("Resolve(clusterIP) = %q, %v", ref, ok)
	}
}

func TestK8sResolver_ExactPodMatch(t *testing.T) {
	r := k8s.NewK8sResolver()
	r.Refresh(testOverlay())

	ref, ok := r.Resolve("10.244.0.5")
	if !ok || ref != k8s.PodRef("c1", "default", "web-abc") {
		t.Errorf("Resolve(podIP) = %q, %v", ref, ok)
	}
}

func TestK8sResolver_PodCIDRFallback(t *testing.T) {
	r := k8s.NewK8sResolver()
	r.Refresh(testOverlay())

	// 10.244.0.42 is inside the pod CIDR but matches no exact pod/service IP.
	ref, ok := r.Resolve("10.244.0.42")
	if !ok || ref != k8s.PodSubnetRef("c1", "k8s-node-1") {
		t.Errorf("Resolve(cidr-only) = %q, %v", ref, ok)
	}
}

func TestK8sResolver_UnknownIPNeverGuesses(t *testing.T) {
	r := k8s.NewK8sResolver()
	r.Refresh(testOverlay())

	if ref, ok := r.Resolve("192.0.2.1"); ok {
		t.Errorf("Resolve(unknown) = %q, %v, want no match", ref, ok)
	}
	if _, ok := r.Resolve("not-an-ip"); ok {
		t.Error("Resolve(garbage) should never match")
	}
}

func TestK8sResolver_RefreshReplacesOnlySameCluster(t *testing.T) {
	r := k8s.NewK8sResolver()
	r.Refresh(testOverlay())
	r.Refresh(k8s.Overlay{
		ClusterID: "c2",
		Services:  []k8s.ServiceInfo{{Namespace: "ns2", Name: "svc2", ClusterIP: "10.96.9.9"}},
	})

	// c1's original service entry must still resolve — refreshing c2 must
	// not have clobbered it.
	if _, ok := r.Resolve("10.96.0.10"); !ok {
		t.Error("c1 service entry was lost after refreshing an unrelated cluster c2")
	}
	if _, ok := r.Resolve("10.96.9.9"); !ok {
		t.Error("c2 service entry did not get indexed")
	}

	// Re-refreshing c1 with an empty overlay must drop its stale entries.
	r.Refresh(k8s.Overlay{ClusterID: "c1"})
	if _, ok := r.Resolve("10.96.0.10"); ok {
		t.Error("stale c1 entry should have been dropped by the empty re-refresh")
	}
	if _, ok := r.Resolve("10.96.9.9"); !ok {
		t.Error("c2 entry should be unaffected by refreshing c1")
	}
}
