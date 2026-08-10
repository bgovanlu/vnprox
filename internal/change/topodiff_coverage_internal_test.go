package change

import (
	"slices"
	"testing"
)

// TestInterfacesByNode_OmittedPathsAreReportedNotDropped gates T-2704's one
// stated scope limit.
//
// The diff compares each node's /etc/network/interfaces and nothing else,
// because that is the only file every snapshot kind captures — a pre/post
// snapshot also carries synthetic SDN config, a scheduled one does not, and
// comparing them would report every SDN zone as newly created on a
// scheduled->pre range. That limit is defensible, but only because the
// omitted paths are NAMED in DiffCoverage.OmittedPaths rather than silently
// dropped: a caller can see what was not compared.
//
// The card shipped with that contract documented in docs/api.md and asserted
// nowhere, so a change that stopped populating OmittedPaths would turn an
// honest disclosure into a silent gap with no test failing. Coverage.Nodes
// and Coverage.UnmatchedNodes were already covered; this closes the third
// field.
func TestInterfacesByNode_OmittedPathsAreReportedNotDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       []snapshotFile
		wantNodes   map[string]string
		wantOmitted []string
	}{
		{
			name: "SDN config alongside interfaces is omitted and named",
			files: []snapshotFile{
				{Node: "pve1", Path: topologyDiffPath, Content: "auto vmbr0\n"},
				{Node: "pve1", Path: "/etc/pve/sdn/zones.cfg", Content: "zone: z1\n"},
				{Node: "pve1", Path: "/etc/pve/sdn/vnets.cfg", Content: "vnet: v1\n"},
			},
			wantNodes:   map[string]string{"pve1": "auto vmbr0\n"},
			wantOmitted: []string{"/etc/pve/sdn/vnets.cfg", "/etc/pve/sdn/zones.cfg"},
		},
		{
			name: "the same omitted path on several nodes is named once",
			files: []snapshotFile{
				{Node: "pve1", Path: topologyDiffPath, Content: "a\n"},
				{Node: "pve2", Path: topologyDiffPath, Content: "b\n"},
				{Node: "pve1", Path: "/etc/pve/sdn/zones.cfg", Content: "z\n"},
				{Node: "pve2", Path: "/etc/pve/sdn/zones.cfg", Content: "z\n"},
			},
			wantNodes:   map[string]string{"pve1": "a\n", "pve2": "b\n"},
			wantOmitted: []string{"/etc/pve/sdn/zones.cfg"},
		},
		{
			name: "interfaces only omits nothing",
			files: []snapshotFile{
				{Node: "pve1", Path: topologyDiffPath, Content: "a\n"},
			},
			wantNodes:   map[string]string{"pve1": "a\n"},
			wantOmitted: nil,
		},
		{
			name: "an interfaces file with no node is not silently compared",
			files: []snapshotFile{
				{Node: "", Path: topologyDiffPath, Content: "orphan\n"},
				{Node: "pve1", Path: topologyDiffPath, Content: "a\n"},
			},
			wantNodes:   map[string]string{"pve1": "a\n"},
			wantOmitted: []string{topologyDiffPath},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			byNode, omitted := interfacesByNode(tt.files)

			if len(byNode) != len(tt.wantNodes) {
				t.Fatalf("byNode = %v, want %v", byNode, tt.wantNodes)
			}
			for node, want := range tt.wantNodes {
				if byNode[node] != want {
					t.Errorf("byNode[%q] = %q, want %q", node, byNode[node], want)
				}
			}

			if !slices.Equal(omitted, tt.wantOmitted) {
				t.Errorf("omitted = %v, want %v — a path that is not compared must be named, "+
					"or the diff's stated scope limit becomes a silent gap", omitted, tt.wantOmitted)
			}
		})
	}
}
