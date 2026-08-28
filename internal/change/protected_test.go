// SPDX-License-Identifier: Apache-2.0

package change

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestLoadProtectedConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadProtectedConfig(filepath.Join(dir, "protected.json"))
	if err != nil {
		t.Fatalf("LoadProtectedConfig: %v", err)
	}
	if cfg.Nodes == nil {
		t.Error("Nodes = nil, want a non-nil empty map for a missing file")
	}
	if len(cfg.Nodes) != 0 {
		t.Errorf("Nodes = %v, want empty", cfg.Nodes)
	}
}

func TestSaveAndLoadProtectedConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vnprox", "protected.json") // nested dir: SaveProtectedConfig must create it

	want := ProtectedConfig{
		Nodes: map[string][]string{
			"pve1": {"bridge:pve1:vmbr0"},
		},
		Version:   1,
		UpdatedAt: 1234,
		UpdatedBy: "root@pam",
	}
	if err := SaveProtectedConfig(path, want); err != nil {
		t.Fatalf("SaveProtectedConfig: %v", err)
	}

	got, err := LoadProtectedConfig(path)
	if err != nil {
		t.Fatalf("LoadProtectedConfig: %v", err)
	}
	if got.UpdatedBy != want.UpdatedBy || got.UpdatedAt != want.UpdatedAt || got.Version != want.Version {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(got.Nodes["pve1"]) != 1 || got.Nodes["pve1"][0] != "bridge:pve1:vmbr0" {
		t.Errorf("Nodes[pve1] = %v, want [bridge:pve1:vmbr0]", got.Nodes["pve1"])
	}

	// No leftover temp file from the atomic-write dance.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want exactly 1 (protected.json, no leftover .tmp): %v", len(entries), entries)
	}
}

func TestProtectedConfig_Resolve(t *testing.T) {
	cfg := ProtectedConfig{Nodes: map[string][]string{
		"pve1": {"bridge:pve1:vmbr0", "not-a-valid-ref"},
	}}
	set, bad := cfg.Resolve()
	if len(bad) != 1 || bad[0] != "not-a-valid-ref" {
		t.Errorf("bad = %v, want [not-a-valid-ref]", bad)
	}
	refs := set["pve1"]
	if len(refs) != 1 || refs[0] != testRef(inventory.KindBridge, "pve1", "vmbr0") {
		t.Errorf("resolved refs = %v, want [bridge:pve1:vmbr0]", refs)
	}
}

func TestDetectProtected(t *testing.T) {
	node1 := &inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"}
	mgmtBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
		Addresses: []string{"10.10.0.1/24"},
	}
	corosyncOnlyVlan := &inventory.VlanIface{
		Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20",
		Addresses: []string{"10.10.1.1/24"},
	}
	unrelatedBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr1"), Name: "vmbr1",
		Addresses: []string{"192.168.50.1/24"},
	}
	snap := buildSnapshot(node1, mgmtBridge, corosyncOnlyVlan, unrelatedBridge)

	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.1", "10.10.1.1"}},
	}}

	set := DetectProtected(snap, cor)
	refs := set["pve1"]
	if len(refs) != 2 {
		t.Fatalf("got %d protected refs for pve1, want 2 (mgmt bridge + corosync vlan): %v", len(refs), refs)
	}
	found := map[inventory.Ref]bool{}
	for _, r := range refs {
		found[r] = true
	}
	if !found[mgmtBridge.Ref] {
		t.Error("management bridge vmbr0 not detected as protected")
	}
	if !found[corosyncOnlyVlan.Ref] {
		t.Error("corosync-only vlan vmbr0.20 not detected as protected")
	}
	if found[unrelatedBridge.Ref] {
		t.Error("unrelated bridge vmbr1 incorrectly detected as protected")
	}
}

func TestDetectProtected_NilCorosyncConfig(t *testing.T) {
	node1 := &inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"}
	mgmtBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
		Addresses: []string{"10.10.0.1/24"},
	}
	snap := buildSnapshot(node1, mgmtBridge)

	set := DetectProtected(snap, nil)
	if len(set["pve1"]) != 1 || set["pve1"][0] != mgmtBridge.Ref {
		t.Errorf("got %v, want just the management bridge detected (nil corosync config falls back to mgmt-IP-only)", set["pve1"])
	}
}

func TestProtectedSet_ToConfig(t *testing.T) {
	set := ProtectedSet{"pve1": {testRef(inventory.KindBridge, "pve1", "vmbr0")}}
	got := set.ToConfig()
	if len(got["pve1"]) != 1 || got["pve1"][0] != "bridge:pve1:vmbr0" {
		t.Errorf("ToConfig = %v, want {pve1: [bridge:pve1:vmbr0]}", got)
	}
}
