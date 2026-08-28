// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath resolves a testdata/clusters/*.yaml path relative to this
// package, so tests work regardless of the working directory `go test` is
// invoked from.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "clusters", name)
}

func TestLoadFixture_AllShippedFixturesValidate(t *testing.T) {
	names := []string{
		"single-node.yaml",
		"three-node-vlan.yaml",
		"evpn-lab.yaml",
		"messy-brownfield.yaml",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f, err := LoadFixture(fixturePath(t, name))
			if err != nil {
				t.Fatalf("LoadFixture(%s): %v", name, err)
			}
			if f.Cluster.Name == "" {
				t.Errorf("%s: cluster.name is empty", name)
			}
			if len(f.Nodes) == 0 {
				t.Errorf("%s: no nodes loaded", name)
			}
		})
	}
}

func TestLoadFixture_MessyBrownfieldDocumentsItsMess(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "messy-brownfield.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(f.Mess) < 5 {
		t.Fatalf("expected messy-brownfield.yaml to document at least 5 specific mess items, got %d", len(f.Mess))
	}
	// The other fixtures should NOT claim any deliberate mess.
	for _, name := range []string{"single-node.yaml", "three-node-vlan.yaml", "evpn-lab.yaml"} {
		clean, err := LoadFixture(fixturePath(t, name))
		if err != nil {
			t.Fatalf("LoadFixture(%s): %v", name, err)
		}
		if len(clean.Mess) != 0 {
			t.Errorf("%s: expected no mess, got %v", name, clean.Mess)
		}
	}
}

// TestLoadFixture_RejectsDanglingReferences proves acceptance criterion 3:
// a bridge referencing a NIC that doesn't exist fails fixture loading with
// a clear, specific error instead of silently loading broken state.
func TestLoadFixture_RejectsDanglingReferences(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "bridge port references nonexistent nic",
			yaml: minimalFixtureYAML(`
      - iface: vmbr0
        type: bridge
        bridge_ports: eno1
`),
			wantErr: "non-existent port",
		},
		{
			name: "bond slave references nonexistent nic",
			yaml: minimalFixtureYAML(`
      - iface: bond0
        type: bond
        slaves: eno1
`),
			wantErr: "non-existent slave",
		},
		{
			name: "vlan parent references nonexistent iface",
			yaml: minimalFixtureYAML(`
      - iface: vmbr0.10
        type: vlan
        vlan_raw_device: vmbr0
        vlan_id: 10
`),
			wantErr: "non-existent parent",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "broken.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFixture(path)
			if err == nil {
				t.Fatalf("expected LoadFixture to fail, got nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func minimalFixtureYAML(networkBlock string) string {
	return `
cluster:
  name: broken
  nodes:
    - {name: n1, ip: 10.0.0.1, online: true}
users:
  - {userid: root@pam, password: x, privileges: ["*"]}
nodes:
  n1:
    network:` + networkBlock
}

func TestLoadFixture_RejectsUnknownNode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	yaml := `
cluster:
  name: broken
  nodes:
    - {name: n1, ip: 10.0.0.1, online: true}
users:
  - {userid: root@pam, password: x, privileges: ["*"]}
nodes:
  n1:
    network: []
  n2:
    network: []
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFixture(path)
	if err == nil || !strings.Contains(err.Error(), "not listed under cluster.nodes") {
		t.Fatalf("expected 'not listed under cluster.nodes' error, got %v", err)
	}
}
