// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// TestCrossClusterConflicts is T-1203 AC2: two clusters sharing an
// overlapping CIDR produce one cross_cluster_duplicate_subnet finding naming
// both clusters; no overlap → empty.
func TestCrossClusterConflicts(t *testing.T) {
	tests := []struct {
		name      string
		clusters  []ipam.ClusterSubnets
		wantPair  []string
		wantCount int
	}{
		{
			name: "identical cidr in two clusters",
			clusters: []ipam.ClusterSubnets{
				{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.10.0.0/24", "192.168.1.0/24"}},
				{ClusterID: "b", ClusterName: "west", CIDRs: []string{"10.10.0.0/24"}},
			},
			wantCount: 1,
			wantPair:  []string{"east", "west"},
		},
		{
			name: "overlapping (supernet/subnet) across clusters",
			clusters: []ipam.ClusterSubnets{
				{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.0.0.0/8"}},
				{ClusterID: "b", ClusterName: "west", CIDRs: []string{"10.10.0.0/24"}},
			},
			wantCount: 1,
			wantPair:  []string{"east", "west"},
		},
		{
			name: "no overlap",
			clusters: []ipam.ClusterSubnets{
				{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.10.0.0/24"}},
				{ClusterID: "b", ClusterName: "west", CIDRs: []string{"10.20.0.0/24"}},
			},
			wantCount: 0,
		},
		{
			name: "overlap within one cluster is not cross-cluster",
			clusters: []ipam.ClusterSubnets{
				{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.0.0.0/8", "10.10.0.0/24"}},
				{ClusterID: "b", ClusterName: "west", CIDRs: []string{"172.16.0.0/24"}},
			},
			wantCount: 0,
		},
		{
			name: "single cluster never conflicts",
			clusters: []ipam.ClusterSubnets{
				{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.10.0.0/24"}},
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ipam.CrossClusterConflicts(tc.clusters)
			if len(got) != tc.wantCount {
				t.Fatalf("got %d conflicts, want %d: %+v", len(got), tc.wantCount, got)
			}
			if tc.wantCount == 0 {
				return
			}
			c := got[0]
			if c.Type != ipam.ConflictCrossClusterDuplicateSubnet {
				t.Errorf("type = %q, want %q", c.Type, ipam.ConflictCrossClusterDuplicateSubnet)
			}
			if len(c.Clusters) != 2 || c.Clusters[0] != tc.wantPair[0] || c.Clusters[1] != tc.wantPair[1] {
				t.Errorf("Clusters = %v, want %v", c.Clusters, tc.wantPair)
			}
		})
	}
}

// TestCrossClusterConflicts_Deterministic proves the same input yields
// byte-identical output (the unified stream's stable-id requirement).
func TestCrossClusterConflicts_Deterministic(t *testing.T) {
	in := []ipam.ClusterSubnets{
		{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.10.0.0/24", "10.20.0.0/24"}},
		{ClusterID: "b", ClusterName: "west", CIDRs: []string{"10.10.0.0/24", "10.20.0.0/24"}},
	}
	first := ipam.CrossClusterConflicts(in)
	for i := 0; i < 5; i++ {
		again := ipam.CrossClusterConflicts(in)
		if len(again) != len(first) {
			t.Fatalf("run %d: len %d != %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j].Message != first[j].Message {
				t.Fatalf("run %d entry %d: %q != %q", i, j, again[j].Message, first[j].Message)
			}
		}
	}
}
