// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestFindings_T803ChecksInUnifiedStream is T-803 AC4's golden test: all
// five new checks appear in the unified GET /findings stream with
// source:"health", a non-empty docsLink, and fixable:false throughout
// (none of these five has a computable fix).
func TestFindings_T803ChecksInUnifiedStream(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")

	// vxlan_underlay_mtu + orphan_vnet + evpn_gw_inconsistency all read
	// SDN/host-netlink state off the same graph, so their fixture setup is
	// batched per ApplyPoll-scope rules (see each check's own test file's
	// doc comments on why).
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "vxlanz"}, ID: "vxlanz", Type: "vxlan", MTU: 1450, Nodes: []string{"pve1"}},
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "evpnz"}, ID: "evpnz", Type: "evpn", Nodes: []string{"pve1", "pve2"}},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "evpnz/vnet-tenant-a"}, ID: "vnet-tenant-a", Zone: "evpnz"},
		&inventory.SdnSubnet{Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "192.168.50.1/24"}, ID: "192.168.50.1/24", Vnet: "vnet-tenant-a", Gateway: "192.168.50.1"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "ghostzone/orphan1"}, ID: "orphan1", Zone: "ghostzone"},
	})
	netlinkPhysNicMTU(g, "pve1", "eno1", 1400) // breaches vxlanz's mtu+overhead
	netlinkVnetBridge(g, "pve1", "vnet-tenant-a", []string{"192.168.50.1/24"})
	netlinkVnetBridge(g, "pve2", "vnet-tenant-a", nil) // dissents -> evpn_gw_inconsistency

	br := trunkBridgeWithVids(g, "pve3", "vmbr0", []inventory.VidRange{{Low: 100, High: 101}})
	guestNicsOn(g, "pve3", []guestNicSpec{{guestID: "200", key: "net0", bridgeRef: br, effectiveVid: 100}})

	corosyncStatus := map[string][]host.RingStatus{
		"pve3": {{RingID: 0, Addr: "10.10.1.13", StatusText: "FAULTY", Faulty: true}},
	}

	eng := findings.New(findings.Config{Graph: g, Corosync: fakeCorosyncProvider{status: corosyncStatus}})
	// Two cycles: clears the hysteresis-gated checks (vxlan_underlay_mtu,
	// corosync_link_degraded); the structural checks (orphan_vnet,
	// evpn_gw_inconsistency, trunk_unused_vlans) fire on the first cycle
	// regardless.
	eng.Findings()
	all := eng.Findings()

	wantChecks := []string{
		findings.CheckVxlanUnderlayMTU,
		findings.CheckOrphanVnet,
		findings.CheckEvpnGwInconsistency,
		findings.CheckCorosyncLinkDegraded,
		findings.CheckTrunkUnusedVlans,
	}
	byCheck := map[string][]findings.Finding{}
	for _, f := range all {
		byCheck[f.Check] = append(byCheck[f.Check], f)
	}

	for _, check := range wantChecks {
		fs := byCheck[check]
		if len(fs) == 0 {
			t.Errorf("check %q produced no findings in the unified stream: %+v", check, all)
			continue
		}
		for _, f := range fs {
			if f.Source != findings.SourceHealth {
				t.Errorf("check %q: Source = %q, want %q", check, f.Source, findings.SourceHealth)
			}
			if f.DocsLink == "" {
				t.Errorf("check %q: missing DocsLink", check)
			}
			if f.Fixable {
				t.Errorf("check %q: Fixable = true, want false (none of T-803's five checks has a computable fix)", check)
			}
		}
	}
}
