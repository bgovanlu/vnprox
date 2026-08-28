// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBridgePortStateFromInt(t *testing.T) {
	cases := []struct {
		want BridgePortSTPState
		n    int
	}{
		{PortStateDisabled, 0},
		{PortStateListening, 1},
		{PortStateLearning, 2},
		{PortStateForwarding, 3},
		{PortStateBlocking, 4},
		{"", 5},
		{"", -1},
	}
	for _, tc := range cases {
		if got := bridgePortStateFromInt(tc.n); got != tc.want {
			t.Errorf("bridgePortStateFromInt(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestDeriveBridgePortRole(t *testing.T) {
	cases := []struct {
		name           string
		state          BridgePortSTPState
		want           BridgePortRole
		portNo         int
		bridgeRootPort int
		bridgeIsRoot   bool
	}{
		// pvecube's actual observed shape (evidence transcript): every
		// bridge is standalone/root (IsRoot true, RootPort 0), and every
		// up port is forwarding — the "designated" case dominates in
		// practice on a real host.
		{name: "root bridge, forwarding port -> designated", state: PortStateForwarding, portNo: 1, bridgeIsRoot: true, bridgeRootPort: 0, want: RoleDesignated},
		{name: "root bridge, disabled (down) port", state: PortStateDisabled, portNo: 1, bridgeIsRoot: true, bridgeRootPort: 0, want: RoleDisabled},
		// Non-root-bridge scenarios (not observed live — no bridge on
		// pvecube runs a real multi-bridge STP topology — but exercise the
		// exact derivation rule documented in deriveBridgePortRole and the
		// evidence transcript's "Field-name/value summary" section.
		{name: "non-root bridge, port_no matches root_port -> root", state: PortStateForwarding, portNo: 1, bridgeIsRoot: false, bridgeRootPort: 1, want: RoleRoot},
		{name: "non-root bridge, other forwarding port -> designated", state: PortStateForwarding, portNo: 2, bridgeIsRoot: false, bridgeRootPort: 1, want: RoleDesignated},
		{name: "non-root bridge, blocking port -> blocking regardless of port_no", state: PortStateBlocking, portNo: 1, bridgeIsRoot: false, bridgeRootPort: 1, want: RoleBlocking},
		{name: "non-root bridge, disabled port -> disabled even if it were the root port", state: PortStateDisabled, portNo: 1, bridgeIsRoot: false, bridgeRootPort: 1, want: RoleDisabled},
		{name: "port_no 0 never matches a nonzero root_port", state: PortStateForwarding, portNo: 0, bridgeIsRoot: false, bridgeRootPort: 1, want: RoleDesignated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveBridgePortRole(tc.state, tc.portNo, tc.bridgeIsRoot, tc.bridgeRootPort)
			if got != tc.want {
				t.Errorf("deriveBridgePortRole(%q, %d, %v, %d) = %q, want %q",
					tc.state, tc.portNo, tc.bridgeIsRoot, tc.bridgeRootPort, got, tc.want)
			}
		})
	}
}

// TestParseBridgeSTP_Pvecube exercises parseBridgeSTP against the exact
// key/value pairs planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt
// recorded for vmbr0/enp1s0 on a live PVE 9.2.4 host — real values, not
// invented ones.
func TestParseBridgeSTP_Pvecube(t *testing.T) {
	bvals := map[string]string{
		"root_id":        "8000.a8b8e0000ee8",
		"bridge_id":      "8000.a8b8e0000ee8",
		"stp_state":      "0",
		"priority":       "32768",
		"root_port":      "0",
		"root_path_cost": "0",
	}
	portVals := map[string]map[string]string{
		"enp1s0": {
			"designated_root":   "8000.a8b8e0000ee8",
			"designated_bridge": "8000.a8b8e0000ee8",
			"designated_cost":   "0",
			"state":             "3",
			"path_cost":         "100",
			"priority":          "32",
			"port_no":           "0x1",
		},
	}

	got := parseBridgeSTP(bvals, portVals)

	if got.RootID != "8000.a8b8e0000ee8" || got.BridgeID != "8000.a8b8e0000ee8" {
		t.Fatalf("RootID/BridgeID = %q/%q, want matching 8000.a8b8e0000ee8", got.RootID, got.BridgeID)
	}
	if got.StpState != 0 {
		t.Errorf("StpState = %d, want 0 (pvecube has bridge-stp off on every bridge)", got.StpState)
	}
	// IsRoot is trivially true here (STP disabled) — this is the exact
	// "misleading" case the evidence transcript and BridgeSTP's doc comment
	// call out; callers must gate display on StpState != 0, not this field.
	if !got.IsRoot {
		t.Errorf("IsRoot = false, want true (RootID == BridgeID)")
	}
	if len(got.Ports) != 1 {
		t.Fatalf("len(Ports) = %d, want 1", len(got.Ports))
	}
	p := got.Ports[0]
	if p.Port != "enp1s0" {
		t.Errorf("Port = %q, want enp1s0", p.Port)
	}
	if p.State != PortStateForwarding {
		t.Errorf("State = %q, want forwarding (sysfs state=3)", p.State)
	}
	if p.PortNo != 1 {
		t.Errorf("PortNo = %d, want 1 (parsed from hex 0x1)", p.PortNo)
	}
	// Root bridge (IsRoot) -> every forwarding port is designated, per
	// deriveBridgePortRole.
	if p.Role != RoleDesignated {
		t.Errorf("Role = %q, want designated", p.Role)
	}
}

// TestParseBridgeSTP_DownPort mirrors the evidence transcript's enp2s0
// (master vmbr1, NO-CARRIER) reading: state=0 -> disabled.
func TestParseBridgeSTP_DownPort(t *testing.T) {
	bvals := map[string]string{
		"root_id":   "8000.a8b8e0000ee9",
		"bridge_id": "8000.a8b8e0000ee9",
		"stp_state": "0",
	}
	portVals := map[string]map[string]string{
		"enp2s0": {
			"designated_root":   "8000.a8b8e0000ee9",
			"designated_bridge": "8000.a8b8e0000ee9",
			"state":             "0",
			"path_cost":         "100",
			"priority":          "32",
			"port_no":           "0x1",
		},
	}
	got := parseBridgeSTP(bvals, portVals)
	if len(got.Ports) != 1 {
		t.Fatalf("len(Ports) = %d, want 1", len(got.Ports))
	}
	if got.Ports[0].State != PortStateDisabled {
		t.Errorf("State = %q, want disabled", got.Ports[0].State)
	}
	if got.Ports[0].Role != RoleDisabled {
		t.Errorf("Role = %q, want disabled", got.Ports[0].Role)
	}
}

// TestParseBridgeSTP_NonRootLoopScenario constructs (not observed live —
// pvecube runs no multi-bridge STP topology, see the evidence transcript's
// "Headline surprise" section) a classic three-port loop-breaking scenario:
// one root port reaching the elected root, one designated port, and one
// port STP is blocking to prevent a loop — the shape deriveBridgePortRole
// exists to compute, exercised end-to-end through parseBridgeSTP.
func TestParseBridgeSTP_NonRootLoopScenario(t *testing.T) {
	bvals := map[string]string{
		"root_id":   "8000.aaaaaaaaaaaa", // a different, lower-priority bridge is root
		"bridge_id": "8000.bbbbbbbbbbbb",
		"stp_state": "1",
		"root_port": "1",
	}
	portVals := map[string]map[string]string{
		"eth0": {"state": "3", "port_no": "0x1"}, // reaches the root -> root port
		"eth1": {"state": "3", "port_no": "0x2"}, // designated
		"eth2": {"state": "4", "port_no": "0x3"}, // blocking (loop-breaking)
	}
	got := parseBridgeSTP(bvals, portVals)
	if got.IsRoot {
		t.Fatalf("IsRoot = true, want false (root_id != bridge_id)")
	}
	roles := map[string]BridgePortRole{}
	for _, p := range got.Ports {
		roles[p.Port] = p.Role
	}
	if roles["eth0"] != RoleRoot {
		t.Errorf("eth0 role = %q, want root", roles["eth0"])
	}
	if roles["eth1"] != RoleDesignated {
		t.Errorf("eth1 role = %q, want designated", roles["eth1"])
	}
	if roles["eth2"] != RoleBlocking {
		t.Errorf("eth2 role = %q, want blocking", roles["eth2"])
	}
}

func TestReadBridgeSTP_SyntheticSysfsTree(t *testing.T) {
	dir := t.TempDir()
	origDir := sysClassNetDir
	sysClassNetDir = dir
	defer func() { sysClassNetDir = origDir }()

	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	brDir := filepath.Join(dir, "vmbr0", "bridge")
	writeFile(filepath.Join(brDir, "root_id"), "8000.a8b8e0000ee8\n")
	writeFile(filepath.Join(brDir, "bridge_id"), "8000.a8b8e0000ee8\n")
	writeFile(filepath.Join(brDir, "stp_state"), "0\n")
	writeFile(filepath.Join(brDir, "priority"), "32768\n")
	writeFile(filepath.Join(brDir, "root_port"), "0\n")
	writeFile(filepath.Join(brDir, "root_path_cost"), "0\n")

	portDir := filepath.Join(dir, "vmbr0", "brif", "enp1s0")
	writeFile(filepath.Join(portDir, "state"), "3\n")
	writeFile(filepath.Join(portDir, "port_no"), "0x1\n")
	writeFile(filepath.Join(portDir, "designated_root"), "8000.a8b8e0000ee8\n")
	writeFile(filepath.Join(portDir, "designated_bridge"), "8000.a8b8e0000ee8\n")
	writeFile(filepath.Join(portDir, "path_cost"), "100\n")
	writeFile(filepath.Join(portDir, "priority"), "32\n")

	got, err := readBridgeSTP("vmbr0")
	if err != nil {
		t.Fatalf("readBridgeSTP: %v", err)
	}
	if got.RootID != "8000.a8b8e0000ee8" {
		t.Errorf("RootID = %q", got.RootID)
	}
	if len(got.Ports) != 1 || got.Ports[0].Port != "enp1s0" {
		t.Fatalf("Ports = %+v", got.Ports)
	}
	if got.Ports[0].State != PortStateForwarding {
		t.Errorf("State = %q, want forwarding", got.Ports[0].State)
	}

	// A bridge with no sysfs directory at all is an error, not a silent
	// zero value.
	if _, err := readBridgeSTP("does-not-exist"); err == nil {
		t.Error("readBridgeSTP(does-not-exist) = nil error, want an error")
	}
}
