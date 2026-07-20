package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// bridgeOp builds a minimal bridge.update op targeting node's vmbr on the
// given node, enough to exercise cross-cluster target scoping.
func bridgeOp(node, bridge string) Op {
	return Op{
		Type:   OpBridgeUpdate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: bridge},
		Params: &BridgeUpdateParams{},
	}
}

// sdnOp builds a cluster-scoped (empty-node) sdn.zone.update op.
func sdnOp(zone string) Op {
	return Op{
		Type:   OpSdnZoneUpdate,
		Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: zone},
		Params: &SdnZoneUpdateParams{},
	}
}

func TestValidateClusterScope(t *testing.T) {
	// membership: pve1/pve2 live in cluster "east", pve9 in "west".
	membership := map[string]string{"pve1": "east", "pve2": "east", "pve9": "west"}

	tests := []struct {
		name        string
		clusterID   string
		nodeCluster map[string]string
		ops         []Op
		wantCodes   []string // codes expected, in order
	}{
		{
			name:        "same-cluster op is clean",
			clusterID:   "east",
			nodeCluster: membership,
			ops:         []Op{bridgeOp("pve1", "vmbr0")},
			wantCodes:   nil,
		},
		{
			name:        "cross-cluster op is rejected with stable code",
			clusterID:   "east",
			nodeCluster: membership,
			ops:         []Op{bridgeOp("pve9", "vmbr0")},
			wantCodes:   []string{codeCrossClusterRef},
		},
		{
			name:        "each offending op yields one finding",
			clusterID:   "east",
			nodeCluster: membership,
			ops:         []Op{bridgeOp("pve1", "vmbr0"), bridgeOp("pve9", "vmbr0"), bridgeOp("pve9", "vmbr1")},
			wantCodes:   []string{codeCrossClusterRef, codeCrossClusterRef},
		},
		{
			name:        "cluster-scoped (empty-node) op never violates scope",
			clusterID:   "east",
			nodeCluster: membership,
			ops:         []Op{sdnOp("zone1")},
			wantCodes:   nil,
		},
		{
			name:        "unknown node is left alone (never guessed)",
			clusterID:   "east",
			nodeCluster: membership,
			ops:         []Op{bridgeOp("pve-unknown", "vmbr0")},
			wantCodes:   nil,
		},
		{
			name:        "implicit default cluster ('') disables the check",
			clusterID:   "",
			nodeCluster: membership,
			ops:         []Op{bridgeOp("pve9", "vmbr0")},
			wantCodes:   nil,
		},
		{
			name:        "no membership known disables the check",
			clusterID:   "east",
			nodeCluster: nil,
			ops:         []Op{bridgeOp("pve9", "vmbr0")},
			wantCodes:   nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateClusterScope(tc.clusterID, tc.ops, tc.nodeCluster)
			if len(got) != len(tc.wantCodes) {
				t.Fatalf("ValidateClusterScope() returned %d findings %+v, want %d", len(got), got, len(tc.wantCodes))
			}
			for i, f := range got {
				if f.Code != tc.wantCodes[i] {
					t.Errorf("finding[%d].Code = %q, want %q", i, f.Code, tc.wantCodes[i])
				}
				if f.Severity != SeverityError {
					t.Errorf("finding[%d].Severity = %q, want error (a cross-cluster op must block)", i, f.Severity)
				}
			}
		})
	}
}
