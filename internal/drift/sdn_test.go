// SPDX-License-Identifier: Apache-2.0

package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestSDNRealization_Missing: a zone lists a node as a member, but that
// node has no matching bridge — detection only, no computable fix.
func TestSDNRealization_Missing(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr99", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr99", 1500, false, nil, nil)
	pveSDNZone(g, "zone-legacy", "vmbr99", []string{"pve1", "pve2", "pve3"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckSDNRealization)
	if len(found) != 1 {
		t.Fatalf("got %d sdn_realization findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("sdn realization gap should not be fixable")
	}
	if f.Severity != drift.SeverityError {
		t.Errorf("severity = %s, want error", f.Severity)
	}
	if !strings.Contains(f.Detail, "zone-legacy") || !strings.Contains(f.Detail, "pve3") {
		t.Errorf("detail = %q, want mention of zone-legacy and pve3", f.Detail)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve3" {
		t.Errorf("nodes = %v, want [pve3]", f.Nodes)
	}
}

// TestSDNRealization_Clean: every member node realizes the zone's bridge.
func TestSDNRealization_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveBridge(g, "pve1", "vmbr99", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr99", 1500, false, nil, nil)
	pveSDNZone(g, "zone-a", "vmbr99", []string{"pve1", "pve2"})

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckSDNRealization); len(found) != 0 {
		t.Errorf("got %d sdn_realization findings on a fully-realized zone, want 0: %+v", len(found), found)
	}
}

// TestSDNZoneStatus_Error (T-3701) proves checkSDNZoneStatus surfaces a
// zone PVE itself reports "error" for, even with no Bridge field at all —
// the "simple" zone shape (Bridge unset) that lets a real zone (labz on
// pvecube) reach this check where checkSDNRealization's bridge-existence
// comparison has nothing to compare.
func TestSDNZoneStatus_Error(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveSDNZoneStatus(g, "labz", map[string]string{"pve1": "error"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckSDNZoneStatus)
	if len(found) != 1 {
		t.Fatalf("got %d sdn_zone_status findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("sdn zone status finding should not be fixable — PVE gives no explanation to build a remedy from")
	}
	if f.Severity != drift.SeverityError {
		t.Errorf("severity = %s, want error", f.Severity)
	}
	if !strings.Contains(f.Detail, "labz") || !strings.Contains(f.Detail, "pve1") || !strings.Contains(f.Detail, "error") {
		t.Errorf("detail = %q, want mention of labz, pve1, and error", f.Detail)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("nodes = %v, want [pve1]", f.Nodes)
	}
}

// TestSDNZoneStatus_Unknown proves a node PVE had nothing to report for
// (the vnprox-synthesized "unknown" status, pve.ReconcileSDNZoneStatus's
// doc comment — confirmed live on a real two-node cluster,
// planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt) raises the
// same error-severity finding a real "error" would, not a downgraded one:
// "no rows for this zone on this node" is not a fact worth trusting less
// urgently than an outright error.
func TestSDNZoneStatus_Unknown(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveSDNZoneStatus(g, "vlanz", map[string]string{"pve1": "unknown"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckSDNZoneStatus)
	if len(found) != 1 {
		t.Fatalf("got %d sdn_zone_status findings, want 1: %+v", len(found), found)
	}
	if found[0].Severity != drift.SeverityError {
		t.Errorf("severity = %s, want error", found[0].Severity)
	}
}

// TestSDNZoneStatus_PendingIsWarningNotError proves an ordinary staged-but-
// unapplied zone (a normal, expected, usually-transient state — already
// visible elsewhere via T-401's own staged-vs-running diff) raises a
// warning, not an error — it must not by itself trip
// internal/change/autorollback.go's error-only auto-rollback gate.
func TestSDNZoneStatus_PendingIsWarningNotError(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pveSDNZoneStatus(g, "vlanz", map[string]string{"pve1": "pending"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckSDNZoneStatus)
	if len(found) != 1 {
		t.Fatalf("got %d sdn_zone_status findings, want 1: %+v", len(found), found)
	}
	if found[0].Severity != drift.SeverityWarning {
		t.Errorf("severity = %s, want warning", found[0].Severity)
	}
}

// TestSDNZoneStatus_Clean: every reported node status is "ok" — no finding.
func TestSDNZoneStatus_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveSDNZoneStatus(g, "vlanz", map[string]string{"pve1": "ok", "pve2": "ok"})

	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckSDNZoneStatus); len(found) != 0 {
		t.Errorf("got %d sdn_zone_status findings on a fully-healthy zone, want 0: %+v", len(found), found)
	}
}
