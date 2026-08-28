// SPDX-License-Identifier: Apache-2.0

package topology

// T-3907: pins Node.Duplex's projection rule — carried whenever
// host-netlink's ethtool read reported one, independent of whether
// SpeedMbps itself is known, mirroring the MediaPort/SpeedMbps independence
// project_media_test.go already pins for the same PhysNic block
// (internal/topology/project.go).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestProject_PhysNic_Duplex(t *testing.T) {
	tests := []struct {
		// Both strings precede the struct: PhysNic has a pointer-free tail,
		// so a string sitting after it extends the bytes govet's
		// fieldalignment counts up to the final pointer.
		name       string
		wantDuplex string
		nic        inventory.PhysNic
	}{
		{
			name:       "full duplex reported",
			nic:        inventory.PhysNic{Name: "eno1", Duplex: "full", SpeedMbps: 1000, LinkUp: true, LinkUpSet: true, OperState: "up"},
			wantDuplex: "full",
		},
		{
			name:       "half duplex reported",
			nic:        inventory.PhysNic{Name: "eno2", Duplex: "half", SpeedMbps: 100, LinkUp: true, LinkUpSet: true, OperState: "up"},
			wantDuplex: "half",
		},
		{
			// No source ever reported a duplex mode: "" is the "never
			// guessed" absence, not a fabricated default — same convention
			// MediaPort's "" case follows.
			name:       "duplex unreported: field absent even though speed is known",
			nic:        inventory.PhysNic{Name: "eno3", SpeedMbps: 1000, LinkUp: true, LinkUpSet: true, OperState: "up"},
			wantDuplex: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: tt.nic.Name}
			tt.nic.Ref = ref
			snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{&tt.nic}})

			topo := Project(snap, Filter{})
			var n *Node
			for i := range topo.Nodes {
				if topo.Nodes[i].ID == ref.String() {
					n = &topo.Nodes[i]
					break
				}
			}
			if n == nil {
				t.Fatalf("node %s not found among %d projected nodes", ref, len(topo.Nodes))
			}
			if n.Duplex != tt.wantDuplex {
				t.Errorf("Duplex = %q, want %q", n.Duplex, tt.wantDuplex)
			}

			b, err := json.Marshal(n)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			gotJSON := string(b)
			if tt.wantDuplex == "" && strings.Contains(gotJSON, `"duplex"`) {
				t.Errorf("json = %s, want no duplex key (duplex unreported)", gotJSON)
			}
			if tt.wantDuplex != "" && !strings.Contains(gotJSON, `"duplex":"`+tt.wantDuplex+`"`) {
				t.Errorf("json = %s, want duplex=%q present", gotJSON, tt.wantDuplex)
			}
		})
	}
}
