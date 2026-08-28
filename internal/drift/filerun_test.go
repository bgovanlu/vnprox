// SPDX-License-Identifier: Apache-2.0

package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestFileRuntimeDivergence_Membership: live (netlink) bridge port
// membership diverges from the declared interfaces file — a manual
// `ip link set <nic> master <bridge>` outside vnprox. Detection only.
func TestFileRuntimeDivergence_Membership(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveBridge(g, "pve1", "vmbr0", 1500, false, nil, []string{"eno1"})
	netlinkBridge(g, "pve1", "vmbr0", 1500, []string{"eno1", "eno3"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d file_runtime_divergence findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("file/runtime membership divergence should not be fixable")
	}
	if !strings.Contains(f.Detail, "eno3") {
		t.Errorf("detail = %q, want mention of eno3 (the manually-attached NIC)", f.Detail)
	}
}

// TestFileRuntimeDivergence_MTU: an entity's own live MTU diverges from
// its declared MTU.
func TestFileRuntimeDivergence_MTU(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)
	netlinkPhysNic(g, "pve1", "eno1", 9000, true)

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d file_runtime_divergence findings, want 1: %+v", len(found), found)
	}
	if !strings.Contains(found[0].Detail, "9000") || !strings.Contains(found[0].Detail, "1500") {
		t.Errorf("detail = %q, want both MTU values", found[0].Detail)
	}
}

// TestFileRuntimeDivergence_FirewallVeths is T-3502's regression test: it
// must fail before the fix (runtimeOwnedMemberPattern / dropRuntimeOwned in
// filerun.go) and pass after it. Both states come from one table so the two
// can't drift apart:
//
//   - "real observed case": pvecube's actual vmbr0, PVE 9.2.4, 2026-08-19 —
//     /etc/network/interfaces declares `bridge-ports enp1s0`, live (netlink)
//     membership is enp1s0,fwpr103p0,fwpr104p0 (pve-firewall's veth peers
//     for the two running LXC guests, both firewall=1). This must produce
//     NO finding — it is PVE's own plumbing, not a manual `ip link` change.
//   - "hand-added member": the same file/live shapes, but with a THIRD live
//     member that does not match PVE's own <vmid>i<netid>/<vmid>p<netid>
//     naming — a genuine `ip link set eno3 master vmbr0` run by a human.
//     This must STILL be reported, and must name eno3, not the fwpr ports.
//
// See planning/reports/evidence/pve-9.2.4-firewall-veths.txt for the full
// transcript (interfaces file, /sys/class/net, ip -d link show, and the
// PVE::Network.pm source the fwpr/fwln/fwbr/tap/veth naming is read from).
func TestFileRuntimeDivergence_FirewallVeths(t *testing.T) {
	// Field order is fieldalignment's, not the reading order: the strings
	// and slices pack ahead of the bool.
	tests := []struct {
		name        string
		wantMention string // substring the finding's detail must contain, if wantFinding
		declared    []string
		live        []string
		dontMention []string
		wantFinding bool
	}{
		{
			name:        "pvecube vmbr0: firewall veths only, no finding",
			declared:    []string{"enp1s0"},
			live:        []string{"enp1s0", "fwpr103p0", "fwpr104p0"},
			wantFinding: false,
		},
		{
			name:        "pvecube vmbr0 plus a hand-added NIC: still reported",
			declared:    []string{"enp1s0"},
			live:        []string{"enp1s0", "fwpr103p0", "fwpr104p0", "eno3"},
			wantFinding: true,
			wantMention: "eno3",
			dontMention: []string{"fwpr103p0", "fwpr104p0"},
		},
		{
			name:        "guest tap device enslaved directly (firewall=0): no finding",
			declared:    []string{"enp2s0"},
			live:        []string{"enp2s0", "tap102i0"},
			wantFinding: false,
		},
		{
			name:        "guest veth device enslaved directly (firewall=0): no finding",
			declared:    []string{"enp2s0"},
			live:        []string{"enp2s0", "veth105i0"},
			wantFinding: false,
		},
		{
			name:        "a literal hand-created veth0 (not PVE's <vmid>i<netid> shape): still reported",
			declared:    []string{"enp1s0"},
			live:        []string{"enp1s0", "veth0"},
			wantFinding: true,
			wantMention: "veth0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGraphWithNodes("pve1")
			pveBridge(g, "pve1", "vmbr0", 1500, false, nil, tt.declared)
			netlinkBridge(g, "pve1", "vmbr0", 1500, tt.live)

			svc := drift.New(drift.Config{Graph: g})
			found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence)

			if !tt.wantFinding {
				if len(found) != 0 {
					t.Fatalf("got %d file_runtime_divergence findings, want 0: %+v", len(found), found)
				}
				return
			}
			if len(found) != 1 {
				t.Fatalf("got %d file_runtime_divergence findings, want 1: %+v", len(found), found)
			}
			detail := found[0].Detail
			if tt.wantMention != "" && !strings.Contains(detail, tt.wantMention) {
				t.Errorf("detail = %q, want mention of %q", detail, tt.wantMention)
			}
			for _, absent := range tt.dontMention {
				if strings.Contains(detail, absent) {
					t.Errorf("detail = %q, must not mention PVE-owned member %q", detail, absent)
				}
			}
		})
	}
}

// TestFileRuntimeDivergence_Clean: matching live and declared state
// produces no findings.
func TestFileRuntimeDivergence_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)
	netlinkPhysNic(g, "pve1", "eno1", 1500, true)
	pveBridge(g, "pve1", "vmbr0", 1500, false, nil, []string{"eno1"})
	netlinkBridge(g, "pve1", "vmbr0", 1500, []string{"eno1"})

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckFileRuntimeDivergence); len(found) != 0 {
		t.Errorf("got %d file_runtime_divergence findings on matching state, want 0: %+v", len(found), found)
	}
}
