package ceph_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// TestStatus_RegistersWithFlowClassifier is T-1503 AC2: a synthetic flow
// crossing Ceph's discovered public/cluster CIDRs classifies as
// ceph-public/ceph-cluster once this package's Status is registered with
// T-1504's flow.Classifier via the generic flow.NewCIDRSource constructor —
// "T-1503 supplies T-1504's engine with Ceph's network declarations",
// exactly as classify.go's own doc comment states this task should wire it
// (no ceph-specific classification logic exists in this package at all).
func TestStatus_RegistersWithFlowClassifier(t *testing.T) {
	_, status := buildSnapshotAndStatus(t, fixtureCephClean)
	if status.PublicNetwork == "" || status.ClusterNetwork == "" {
		t.Fatalf("expected discovered public/cluster CIDRs, got %+v", status)
	}

	classifier := flow.NewClassifier()

	pubSrc, err := flow.NewCIDRSource(flow.NetworkSourceKindCeph, flow.ServiceClassCephPublic, []string{status.PublicNetwork}, nil)
	if err != nil {
		t.Fatalf("NewCIDRSource(public): %v", err)
	}
	classifier.RegisterNetworkSource(flow.NetworkSourceKindCeph, pubSrc)

	clusterSrc, err := flow.NewCIDRSource(flow.NetworkSourceKindCeph, flow.ServiceClassCephCluster, []string{status.ClusterNetwork}, nil)
	if err != nil {
		t.Fatalf("NewCIDRSource(cluster): %v", err)
	}
	classifier.RegisterNetworkSource(flow.NetworkSourceKindCeph, clusterSrc)

	tests := []struct {
		name string
		want flow.ServiceClass
		rec  flow.Record
	}{
		{
			name: "flow inside Ceph public CIDR",
			rec:  flow.Record{SrcIP: "10.20.0.11", DstIP: "10.20.0.12", Proto: 6, DstPort: 6789},
			want: flow.ServiceClassCephPublic,
		},
		{
			name: "flow inside Ceph cluster CIDR",
			rec:  flow.Record{SrcIP: "10.30.0.11", DstIP: "10.30.0.12", Proto: 6, DstPort: 6800},
			want: flow.ServiceClassCephCluster,
		},
		{
			name: "flow outside both CIDRs classifies unclassified",
			rec:  flow.Record{SrcIP: "10.10.0.11", DstIP: "10.10.0.12", Proto: 6, DstPort: 22},
			want: flow.ServiceClassUnclassified,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifier.Classify(tt.rec); got != tt.want {
				t.Errorf("Classify(%+v) = %s, want %s", tt.rec, got, tt.want)
			}
		})
	}
}
