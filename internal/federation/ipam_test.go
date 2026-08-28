// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const fixtureIpamLab = "../../testdata/clusters/ipam-lab.yaml"

// TestAggregator_IPAMSubnets fans the SDN subnet enumeration out to two
// attached clusters (both the ipam-lab fixture, so both configure
// 10.50.0.0/24) and confirms each cluster's CIDR set comes back tagged with
// its own clusterId — the per-cluster input the cross-cluster duplicate-subnet
// check (T-1203) folds together.
func TestAggregator_IPAMSubnets(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "east", FixturePath: fixtureIpamLab},
		{Name: "west", FixturePath: fixtureIpamLab},
	})
	agg := NewAggregator(svc)

	results, partial, failed, err := agg.IPAMSubnets(context.Background())
	if err != nil {
		t.Fatalf("IPAMSubnets: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("both reachable but partial=%v failed=%v", partial, failed)
	}
	if len(results) != 2 {
		t.Fatalf("got %d cluster subnet sets, want 2", len(results))
	}
	byID := map[string][]string{}
	for _, r := range results {
		byID[r.ClusterID] = r.CIDRs
	}
	for name, id := range ids {
		cidrs, ok := byID[id]
		if !ok {
			t.Fatalf("no subnet set for cluster %q (id %s)", name, id)
		}
		if !containsStr(cidrs, "10.50.0.0/24") {
			t.Errorf("cluster %q CIDRs = %v, want to contain 10.50.0.0/24", name, cidrs)
		}
	}
}

// TestAggregator_IPAMSubnets_FailureIsolation kills one of two clusters and
// confirms the survivor's subnet set is intact with partial/failedClusters
// set — the same isolation guarantee ClusterNodesAll has.
func TestAggregator_IPAMSubnets_FailureIsolation(t *testing.T) {
	svc, _, _ := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "east", FixturePath: fixtureIpamLab},
		{Name: "west", FixturePath: fixtureIpamLab},
	})
	agg := NewAggregator(svc)

	group.ByName("west").Close()

	results, partial, failed, err := agg.IPAMSubnets(context.Background())
	if err != nil {
		t.Fatalf("IPAMSubnets: %v", err)
	}
	if !partial {
		t.Error("partial = false, want true (one cluster unreachable)")
	}
	if len(failed) != 1 || failed[0] != ids["west"] {
		t.Fatalf("failedClusters = %v, want [%s]", failed, ids["west"])
	}
	if len(results) != 1 || results[0].ClusterID != ids["east"] {
		t.Fatalf("results = %+v, want just the east cluster", results)
	}
	if !containsStr(results[0].CIDRs, "10.50.0.0/24") {
		t.Errorf("survivor CIDRs = %v, want to contain 10.50.0.0/24", results[0].CIDRs)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
