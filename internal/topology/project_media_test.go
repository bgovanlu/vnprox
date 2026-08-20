package topology

// T-3503: pins the media-port/speed independence rule the evidence
// transcript (planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt)
// settles — a physnic's mediaPort must reach the wire even when its
// speedMbps is unknown (down link, e.g. an unplugged fibre/DA port), and
// the two fields must never be conflated by a shared guard. Built the same
// way status_internal_test.go's PhysNic tests are: hand-constructed
// entities pushed through the real Graph.ApplyPoll merge (snapshotOf, in
// status_internal_test.go), not a faked snapshot — so this exercises the
// same ownership/pick machinery a live poll does.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestProject_PhysNic_MediaPortIndependentOfSpeed(t *testing.T) {
	// Field order is fieldalignment's, not the reading order: the strings
	// and the (pointer-bearing) PhysNic pack ahead of the int.
	tests := []struct {
		name          string
		wantMediaPort string
		nic           inventory.PhysNic
		wantSpeedMbps int
	}{
		{
			// The evidence transcript's exact case: a down fibre/DA link
			// still reports its media type (pvecube's down enp2s0/enp4s0
			// both answered "Port: Twisted Pair" with Speed Unknown; this
			// case exercises the fibre branch the evidence file notes
			// pvecube's all-TP hardware can't, per
			// planning/reports/needs-hardware-validation.md).
			name:          "down fibre link: mediaPort present, speedMbps absent",
			nic:           inventory.PhysNic{Name: "sfp0", MediaPort: "fibre", SpeedMbps: 0, LinkUp: false, LinkUpSet: true, OperState: "down"},
			wantMediaPort: "fibre",
			wantSpeedMbps: 0,
		},
		{
			name:          "up copper link: both present",
			nic:           inventory.PhysNic{Name: "eno1", MediaPort: "tp", SpeedMbps: 1000, LinkUp: true, LinkUpSet: true, OperState: "up"},
			wantMediaPort: "tp",
			wantSpeedMbps: 1000,
		},
		{
			// No source ever reported a media type (e.g. pve-network-only
			// data, or a platform/driver that couldn't answer the ioctl):
			// "" is the "never guessed" absence, not a fabricated default.
			name:          "no media reported: field absent",
			nic:           inventory.PhysNic{Name: "eno2", SpeedMbps: 1000, LinkUp: true, LinkUpSet: true, OperState: "up"},
			wantMediaPort: "",
			wantSpeedMbps: 1000,
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
			if n.MediaPort != tt.wantMediaPort {
				t.Errorf("MediaPort = %q, want %q", n.MediaPort, tt.wantMediaPort)
			}
			if n.SpeedMbps != tt.wantSpeedMbps {
				t.Errorf("SpeedMbps = %d, want %d", n.SpeedMbps, tt.wantSpeedMbps)
			}

			// Pin the wire shape too: omitempty must actually drop
			// speedMbps when it's the zero value, and never drop a
			// present mediaPort — the exact conflation the evidence file
			// rules out (down copper flipping to a drawn SFP cage, or a
			// down fibre link losing its port body entirely).
			b, err := json.Marshal(n)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			gotJSON := string(b)
			if tt.wantSpeedMbps == 0 && strings.Contains(gotJSON, `"speedMbps"`) {
				t.Errorf("json = %s, want no speedMbps key (speed unknown)", gotJSON)
			}
			if tt.wantMediaPort == "" && strings.Contains(gotJSON, `"mediaPort"`) {
				t.Errorf("json = %s, want no mediaPort key (media unreported)", gotJSON)
			}
			if tt.wantMediaPort != "" && !strings.Contains(gotJSON, `"mediaPort":"`+tt.wantMediaPort+`"`) {
				t.Errorf("json = %s, want mediaPort=%q present", gotJSON, tt.wantMediaPort)
			}
		})
	}
}
