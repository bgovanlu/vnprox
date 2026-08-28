// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"os"
	"testing"
)

// readOVSFixture reads one of testdata/ovsvsctl/<scenario>/{bridge,port,
// interface}.json — synthesized-but-faithful samples of `ovs-vsctl -f json
// --columns=... list <table>` output (T-407 AC4's "synthesize realistic
// ovs-vsctl output samples" corpus).
func readOVSFixture(t *testing.T, scenario, table string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/ovsvsctl/" + scenario + "/" + table + ".json")
	if err != nil {
		t.Fatalf("reading ovsvsctl fixture %s/%s: %v", scenario, table, err)
	}
	return data
}

// TestBuildOVSBridgeStatus_Basic parses the "basic" corpus scenario (one
// bridge with an OVS bond port — two interfaces — and a tagged Int Port,
// matching testdata/clusters/ovs-lab.yaml's declared config) and asserts
// the full joined tree: bridge -> ports -> interfaces, tag/trunks/bond_mode,
// link_state, and per-interface counters.
func TestBuildOVSBridgeStatus_Basic(t *testing.T) {
	bridges, err := BuildOVSBridgeStatus(
		readOVSFixture(t, "basic", "bridge"),
		readOVSFixture(t, "basic", "port"),
		readOVSFixture(t, "basic", "interface"),
	)
	if err != nil {
		t.Fatalf("BuildOVSBridgeStatus: %v", err)
	}
	if len(bridges) != 1 {
		t.Fatalf("got %d bridge(s), want 1", len(bridges))
	}
	br := bridges[0]
	if br.Name != "vmbr1" {
		t.Errorf("bridge name = %q, want vmbr1", br.Name)
	}
	if len(br.Ports) != 2 {
		t.Fatalf("got %d port(s), want 2", len(br.Ports))
	}

	var bond, intPort *OVSPortStatus
	for i := range br.Ports {
		switch br.Ports[i].Name {
		case "bond0":
			bond = &br.Ports[i]
		case "vlan30":
			intPort = &br.Ports[i]
		}
	}
	if bond == nil {
		t.Fatal("missing port bond0")
	}
	if bond.BondMode != "active-backup" {
		t.Errorf("bond0 BondMode = %q, want active-backup", bond.BondMode)
	}
	if len(bond.Interfaces) != 2 {
		t.Fatalf("bond0 has %d interface(s), want 2", len(bond.Interfaces))
	}
	byName := map[string]OVSInterfaceStatus{}
	for _, ifc := range bond.Interfaces {
		byName[ifc.Name] = ifc
	}
	eno3, ok := byName["eno3"]
	if !ok {
		t.Fatal("missing bond0 member eno3")
	}
	if eno3.Type != "system" || eno3.LinkState != "up" {
		t.Errorf("eno3 = %+v, want type system, link_state up", eno3)
	}
	if eno3.RxBytes != 209715200 || eno3.TxBytes != 104857600 || eno3.RxPackets != 180000 || eno3.TxPackets != 90000 {
		t.Errorf("eno3 counters = %+v, want rx=209715200/180000 tx=104857600/90000", eno3)
	}
	if _, ok := byName["eno4"]; !ok {
		t.Error("missing bond0 member eno4")
	}

	if intPort == nil {
		t.Fatal("missing port vlan30")
	}
	if intPort.Tag != 30 {
		t.Errorf("vlan30 Tag = %d, want 30", intPort.Tag)
	}
	if len(intPort.Trunks) != 0 {
		t.Errorf("vlan30 Trunks = %v, want none", intPort.Trunks)
	}
	if len(intPort.Interfaces) != 1 || intPort.Interfaces[0].Type != "internal" {
		t.Errorf("vlan30 interfaces = %+v, want one internal-type interface", intPort.Interfaces)
	}
}

// TestBuildOVSBridgeStatus_Empty parses the "empty" corpus scenario (a
// bridge configured with zero ports — the degenerate-but-valid OVSDB
// "set" encoding for "nothing here": ["set",[]]) and asserts it decodes
// to a bridge with a nil ports slice rather than erroring.
func TestBuildOVSBridgeStatus_Empty(t *testing.T) {
	bridges, err := BuildOVSBridgeStatus(
		readOVSFixture(t, "empty", "bridge"),
		readOVSFixture(t, "empty", "port"),
		readOVSFixture(t, "empty", "interface"),
	)
	if err != nil {
		t.Fatalf("BuildOVSBridgeStatus: %v", err)
	}
	if len(bridges) != 1 || bridges[0].Name != "vmbr9" {
		t.Fatalf("bridges = %+v, want one bridge named vmbr9", bridges)
	}
	if len(bridges[0].Ports) != 0 {
		t.Errorf("vmbr9 Ports = %v, want none", bridges[0].Ports)
	}
}

// TestBuildOVSBridgeStatus_Malformed table-tests rejection of malformed
// table JSON at each of the three parse stages, so a truncated/corrupted
// ovs-vsctl invocation surfaces a clear error instead of a silent
// misparse or panic.
func TestBuildOVSBridgeStatus_Malformed(t *testing.T) {
	valid := func(scenario, table string) []byte { return readOVSFixture(t, scenario, table) }
	tests := []struct {
		name                         string
		bridgeJSON, portJSON, ifJSON []byte
	}{
		{
			name:       "not json",
			bridgeJSON: []byte("not json"),
			portJSON:   valid("basic", "port"),
			ifJSON:     valid("basic", "interface"),
		},
		{
			name:       "bridge row wrong column count",
			bridgeJSON: []byte(`{"headings":["name","ports"],"data":[["vmbr1"]]}`),
			portJSON:   valid("basic", "port"),
			ifJSON:     valid("basic", "interface"),
		},
		{
			name:       "port tag not a recognized shape",
			bridgeJSON: valid("basic", "bridge"),
			portJSON:   []byte(`{"headings":["_uuid","name","tag","trunks","bond_mode","interfaces"],"data":[[["uuid","x"],"p0",{"bogus":true},["set",[]],"active-backup",["set",[]]]]}`),
			ifJSON:     valid("basic", "interface"),
		},
		{
			name:       "interface statistics not a map",
			bridgeJSON: valid("basic", "bridge"),
			portJSON:   valid("basic", "port"),
			ifJSON:     []byte(`{"headings":["_uuid","name","type","link_state","statistics"],"data":[[["uuid","3f8a1c20-0000-4000-8000-000000000i03"],"eno3","system","up","not-a-map"]]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildOVSBridgeStatus(tt.bridgeJSON, tt.portJSON, tt.ifJSON); err == nil {
				t.Fatal("BuildOVSBridgeStatus: expected error, got nil")
			}
		})
	}
}

// TestReal_OVSStatus_ToolingAbsent is T-407 AC4's graceful-degradation
// case: when ovs-vsctl is not installed (exec.LookPath fails to resolve
// it), OVSStatus returns ErrOVSUnavailable rather than a bare exec error —
// mirroring Real.LLDP's identical ErrLLDPUnavailable contract exactly, so
// callers can degrade to a config-only view without treating "never
// installed" as a poll failure worth logging/alerting on every cycle.
func TestReal_OVSStatus_ToolingAbsent(t *testing.T) {
	r := &Real{OVSVSCtlPath: "vnprox-definitely-not-a-real-binary-xyz"}
	_, err := r.OVSStatus(context.Background(), "pve1")
	if err == nil {
		t.Fatal("OVSStatus: expected error for missing binary, got nil")
	}
	if !errors.Is(err, ErrOVSUnavailable) {
		t.Fatalf("OVSStatus error = %v, want wrapping ErrOVSUnavailable", err)
	}
}
