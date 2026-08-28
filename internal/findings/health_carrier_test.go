// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// TestBridgeNoCarrier: a bridge whose only uplink NIC has no carrier fires
// CheckBridgeNoCarrier after hysteresis clears (AC1).
func TestBridgeNoCarrier(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkPhysNicUp(g, "pve1", "eno1", false)
	netlinkBridgeWithPorts(g, "pve1", "vmbr0", []string{"eno1"})

	eng := findings.New(findings.Config{Graph: g})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckBridgeNoCarrier)
	if len(found) != 1 {
		t.Fatalf("got %d bridge_no_carrier findings after 2 cycles, want 1", len(found))
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("bridge_no_carrier should not be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("bridge_no_carrier must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "vmbr0") || !strings.Contains(f.Detail, "pve1") || !strings.Contains(f.Detail, "no carrier") {
		t.Errorf("detail = %q, want mention of vmbr0/pve1/no carrier", f.Detail)
	}
}

// TestBridgeCarrier_OneOfTwoUp_NoFinding: a bond-backed bridge with at
// least one live uplink never fires, even if another uplink is down.
func TestBridgeCarrier_OneUplinkUp_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkPhysNics(g, "pve1", map[string]bool{"eno1": true, "eno2": false})
	netlinkBridgeWithPorts(g, "pve1", "vmbr0", []string{"eno1", "eno2"})

	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckBridgeNoCarrier); len(found) != 0 {
			t.Fatalf("cycle %d: bridge with one live uplink produced a no-carrier finding: %+v", i, found)
		}
	}
}

// TestBridgeCarrier_NoPorts_NeverFlagged: a bridge with zero configured
// ports (a pure NAT/internal bridge) is never flagged — it was never meant
// to have an uplink.
func TestBridgeCarrier_NoPorts_NeverFlagged(t *testing.T) {
	g := newGraphWithNodes("pve1")
	netlinkBridgeWithPorts(g, "pve1", "vmbr99", nil)

	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckBridgeNoCarrier); len(found) != 0 {
			t.Fatalf("cycle %d: portless bridge produced a no-carrier finding: %+v", i, found)
		}
	}
}
