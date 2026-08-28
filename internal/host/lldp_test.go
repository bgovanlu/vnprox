// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"testing"
)

func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "lldp", name))
	if err != nil {
		t.Fatalf("reading testdata/lldp/%s: %v", name, err)
	}
	return data
}

func TestParseLLDP_NestedBasic(t *testing.T) {
	neighbors, err := ParseLLDP(mustReadTestdata(t, "nested_basic.json"))
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("got %d neighbors, want 1", len(neighbors))
	}
	n := neighbors[0]
	if n.LocalIface != "eno1" || n.Protocol != "LLDP" {
		t.Errorf("localIface/protocol = %q/%q, want eno1/LLDP", n.LocalIface, n.Protocol)
	}
	if n.ChassisID != "ac:1f:6b:01:00:01" || n.ChassisName != "sw-core-01" {
		t.Errorf("chassis = %q/%q, want ac:1f:6b:01:00:01/sw-core-01", n.ChassisID, n.ChassisName)
	}
	if n.ChassisIDType != "mac" {
		t.Errorf("chassisIDType = %q, want mac", n.ChassisIDType)
	}
	if len(n.MgmtIPs) != 1 || n.MgmtIPs[0] != "10.10.0.254" {
		t.Errorf("mgmtIPs = %v, want [10.10.0.254]", n.MgmtIPs)
	}
	if n.PortID != "Te1/0/1" || n.PortIDType != "ifname" {
		t.Errorf("port = %q/%q, want Te1/0/1/ifname", n.PortID, n.PortIDType)
	}
	if n.TTL != 120 {
		t.Errorf("ttl = %d, want 120", n.TTL)
	}
	if n.PVID != 10 {
		t.Errorf("pvid = %d, want 10", n.PVID)
	}
	if len(n.TaggedVLANs) != 2 || n.TaggedVLANs[0] != 20 || n.TaggedVLANs[1] != 30 {
		t.Errorf("taggedVLANs = %v, want [20 30]", n.TaggedVLANs)
	}
	if n.SpeedMbps != 1000 {
		t.Errorf("speedMbps = %d, want 1000", n.SpeedMbps)
	}
}

func TestParseLLDP_NestedCDP(t *testing.T) {
	neighbors, err := ParseLLDP(mustReadTestdata(t, "nested_cdp.json"))
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("got %d neighbors, want 1", len(neighbors))
	}
	n := neighbors[0]
	if n.Protocol != "CDPv2" {
		t.Errorf("protocol = %q, want CDPv2", n.Protocol)
	}
	if n.ChassisName != "old-switch-01.example.com" {
		t.Errorf("chassisName = %q", n.ChassisName)
	}
	// Single-item mgmt-ip collapsed to a bare object, not an array.
	if len(n.MgmtIPs) != 1 || n.MgmtIPs[0] != "10.10.0.200" {
		t.Errorf("mgmtIPs = %v, want [10.10.0.200]", n.MgmtIPs)
	}
	// Bare numeric ttl (not a string).
	if n.TTL != 180 {
		t.Errorf("ttl = %d, want 180", n.TTL)
	}
}

func TestParseLLDP_NestedCollapsedSingletons(t *testing.T) {
	neighbors, err := ParseLLDP(mustReadTestdata(t, "nested_collapsed_singletons.json"))
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("got %d neighbors, want 1: %+v", len(neighbors), neighbors)
	}
	n := neighbors[0]
	if n.ChassisName != "sw-access-01" {
		t.Errorf("chassisName = %q", n.ChassisName)
	}
	if len(n.MgmtIPs) != 1 || n.MgmtIPs[0] != "10.10.0.240" {
		t.Errorf("mgmtIPs = %v", n.MgmtIPs)
	}
	if n.PVID != 10 {
		t.Errorf("pvid = %d, want 10 (numeric vlan-id, collapsed vlan object)", n.PVID)
	}
}

func TestParseLLDP_FlatLegacy(t *testing.T) {
	neighbors, err := ParseLLDP(mustReadTestdata(t, "flat_legacy.json"))
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("got %d neighbors, want 2", len(neighbors))
	}
	if neighbors[0].LocalIface != "eno1" || neighbors[0].ChassisID != "ac:1f:6b:01:00:01" {
		t.Errorf("neighbor 0 = %+v", neighbors[0])
	}
	if neighbors[0].PVID != 10 {
		t.Errorf("neighbor 0 pvid = %d, want 10 (flat 'vlan' maps to PVID)", neighbors[0].PVID)
	}
}

// TestParseLLDP_FlatTaggedVLANs is T-403's LLDP trunk cross-check plumbing:
// the flat fixture schema (internal/pvemock's marshalLLDP output) needs to
// carry a switch port's trunked VLANs, not just its native PVID, for the
// VLAN zone wizard's "does the physical path actually trunk this VID"
// check to have anything to check against.
func TestParseLLDP_FlatTaggedVLANs(t *testing.T) {
	raw := []byte(`[{"local-iface":"eno1","chassis_name":"sw-core-01","chassis_id":"ac:1f:6b:01:00:01","port_id":"Te1/0/1","vlan":10,"tagged_vlans":[100,200,300],"ttl":120}]`)
	neighbors, err := ParseLLDP(raw)
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("got %d neighbors, want 1", len(neighbors))
	}
	n := neighbors[0]
	if n.PVID != 10 {
		t.Errorf("pvid = %d, want 10", n.PVID)
	}
	want := []int{100, 200, 300}
	if len(n.TaggedVLANs) != len(want) {
		t.Fatalf("taggedVlans = %v, want %v", n.TaggedVLANs, want)
	}
	for i, v := range want {
		if n.TaggedVLANs[i] != v {
			t.Errorf("taggedVlans[%d] = %d, want %d", i, n.TaggedVLANs[i], v)
		}
	}
}

// TestParseLLDP_FlatTaggedVLANsAbsent confirms a fixture predating T-403
// (no "tagged_vlans" key at all) still parses cleanly with a nil/empty
// TaggedVLANs — the flat schema's addition is backward-compatible.
func TestParseLLDP_FlatTaggedVLANsAbsent(t *testing.T) {
	neighbors, err := ParseLLDP(mustReadTestdata(t, "flat_legacy.json"))
	if err != nil {
		t.Fatalf("ParseLLDP: %v", err)
	}
	if len(neighbors[0].TaggedVLANs) != 0 {
		t.Errorf("taggedVlans = %v, want empty (fixture predates tagged_vlans)", neighbors[0].TaggedVLANs)
	}
}

func TestParseLLDP_Empty(t *testing.T) {
	neighbors, err := ParseLLDP(nil)
	if err != nil || neighbors != nil {
		t.Errorf("ParseLLDP(nil) = %v, %v, want nil, nil", neighbors, err)
	}
	neighbors, err = ParseLLDP([]byte("   \n  "))
	if err != nil || neighbors != nil {
		t.Errorf("ParseLLDP(whitespace) = %v, %v, want nil, nil", neighbors, err)
	}
}

// TestParseLLDP_Adversarial walks every file in testdata/lldp/adversarial and
// asserts only that ParseLLDP never panics — it may return an error or a
// best-effort partial/empty result, both are acceptable (T-302 AC4).
func TestParseLLDP_Adversarial(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "lldp", "adversarial"))
	if err != nil {
		t.Fatalf("reading adversarial corpus dir: %v", err)
	}
	for _, ent := range entries {
		ent := ent
		t.Run(ent.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "lldp", "adversarial", ent.Name()))
			if err != nil {
				t.Fatalf("reading %s: %v", ent.Name(), err)
			}
			// The only invariant: never panic. Error return is fine.
			_, _ = ParseLLDP(data)
		})
	}
}
