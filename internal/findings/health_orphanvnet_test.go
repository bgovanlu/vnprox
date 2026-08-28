// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestOrphanVnet_Fires: a vnet whose zone no longer exists fires immediately
// (hysteresis-exempt — structural, not a noisy counter).
func TestOrphanVnet_Fires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	sdnZone(g, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "realzone"},
		ID:  "realzone", Type: "simple",
	})
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnVnet{
			Ref:  inventory.Ref{Kind: inventory.KindSDNVnet, ID: "ghostzone/orphan1"},
			ID:   "orphan1",
			Zone: "ghostzone",
		},
	})

	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckOrphanVnet)
	if len(found) != 1 {
		t.Fatalf("got %d orphan_vnet findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("orphan_vnet should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("orphan_vnet must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "orphan1") || !strings.Contains(f.Detail, "ghostzone") {
		t.Errorf("detail = %q, want mention of orphan1/ghostzone", f.Detail)
	}
}

// TestOrphanVnet_ExistingZone_NoFinding: a vnet whose zone still exists
// never fires, even alongside an orphaned one.
func TestOrphanVnet_ExistingZone_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "realzone"}, ID: "realzone", Type: "simple"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "realzone/vnet1"}, ID: "vnet1", Zone: "realzone"},
	})

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckOrphanVnet); len(found) != 0 {
		t.Fatalf("vnet with a live zone produced a finding: %+v", found)
	}
}

// TestOrphanVnet_ClearsWhenZoneRecreated: the finding clears the instant the
// referenced zone reappears (no lingering hysteresis state, since this
// check is stateless/hysteresis-exempt).
func TestOrphanVnet_ClearsWhenZoneRecreated(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "ghostzone/orphan1"}, ID: "orphan1", Zone: "ghostzone"},
	})
	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckOrphanVnet); len(found) != 1 {
		t.Fatalf("setup: expected the finding active before testing recovery, got %d", len(found))
	}

	// Scope{} reconciles every cluster-scoped SourcePVESDN entity in one
	// shot, so the vnet must be re-supplied alongside the now-recreated
	// zone in the same poll, not retired by omission.
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "ghostzone"}, ID: "ghostzone", Type: "simple"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "ghostzone/orphan1"}, ID: "orphan1", Zone: "ghostzone"},
	})
	if found := findByCheck(t, eng.Findings(), findings.CheckOrphanVnet); len(found) != 0 {
		t.Fatalf("finding did not clear once the zone was recreated: %+v", found)
	}
}
