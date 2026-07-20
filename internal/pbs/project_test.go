package pbs_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pbs"
)

// TestProject_DiscoversPBSHost is T-1206 AC1's golden projection: the
// backup-paths fixture's single PBS storage entry produces exactly one
// pbs-host with the correct cluster-scoped Ref and fields.
func TestProject_DiscoversPBSHost(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureBackupPaths)
	overlay := pbs.Project(snap, status)

	if len(overlay.Hosts) != 1 {
		t.Fatalf("overlay.Hosts = %d, want 1", len(overlay.Hosts))
	}
	h := overlay.Hosts[0]
	if h.Ref != (inventory.Ref{Kind: inventory.KindPBSHost, ID: "10.50.0.9"}) {
		t.Errorf("host Ref = %+v, want pbs-host::10.50.0.9 (cluster-scoped)", h.Ref)
	}
	if h.Ref.Node != "" {
		t.Errorf("pbs-host must be cluster-scoped (empty Node), got Node=%q", h.Ref.Node)
	}
	if h.Address != "10.50.0.9" {
		t.Errorf("host Address = %q, want 10.50.0.9", h.Address)
	}
	if len(h.Datastores) != 1 || h.Datastores[0] != "main" {
		t.Errorf("host Datastores = %v, want [main]", h.Datastores)
	}
	if len(h.StorageIDs) != 1 || h.StorageIDs[0] != "pbs-main" {
		t.Errorf("host StorageIDs = %v, want [pbs-main]", h.StorageIDs)
	}
	if h.Fingerprint == "" {
		t.Errorf("host Fingerprint should be carried from storage.cfg, got empty")
	}
}

// TestProject_BackupPathPresentForBackingUpNodeOnly is T-1206 AC2: a
// backup-path exists for the node with a backup job targeting the storage
// (pve1), and is absent for the node with none (pve2). The path resolves to
// pve1's routed egress (vmbr0) and its riding bond (bond0).
func TestProject_BackupPathPresentForBackingUpNodeOnly(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureBackupPaths)
	overlay := pbs.Project(snap, status)

	byNode := map[string]pbs.BackupPath{}
	for _, p := range overlay.Paths {
		byNode[p.Node] = p
	}

	if len(overlay.Paths) != 1 {
		t.Fatalf("overlay.Paths = %d, want exactly 1 (pve1 only)", len(overlay.Paths))
	}
	p, ok := byNode["pve1"]
	if !ok {
		t.Fatalf("no backup path for pve1 (which has a backup job targeting pbs-main)")
	}
	if _, ok := byNode["pve2"]; ok {
		t.Errorf("pve2 has no backup job targeting pbs-main; it must have no backup path")
	}

	wantHost := inventory.Ref{Kind: inventory.KindPBSHost, ID: "10.50.0.9"}
	if p.Host != wantHost {
		t.Errorf("path.Host = %+v, want %+v", p.Host, wantHost)
	}
	if p.Carrier != (inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}) {
		t.Errorf("path.Carrier = %+v, want bridge:pve1:vmbr0 (routed egress via gateway bridge)", p.Carrier)
	}
	if p.RidingOn != (inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}) {
		t.Errorf("path.RidingOn = %+v, want bond:pve1:bond0", p.RidingOn)
	}
	if !p.LinkKnown || p.LinkMbps != 10000 {
		t.Errorf("path link = %d mbps (known=%v), want 10000 known", p.LinkMbps, p.LinkKnown)
	}
	if len(p.StorageIDs) != 1 || p.StorageIDs[0] != "pbs-main" {
		t.Errorf("path.StorageIDs = %v, want [pbs-main]", p.StorageIDs)
	}
	if len(p.Jobs) != 1 || p.Jobs[0].ID != "backup-daily" {
		t.Errorf("path.Jobs = %+v, want one job backup-daily", p.Jobs)
	}
}

// TestProject_SizingHintDeterministic is T-1206 AC3: the datastore-network
// sizing hint is a fixed, plain-English string given fixed fixture inputs,
// and is explicitly flagged as a heuristic estimate.
func TestProject_SizingHintDeterministic(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureBackupPaths)
	overlay := pbs.Project(snap, status)

	if len(overlay.Paths) != 1 {
		t.Fatalf("overlay.Paths = %d, want 1", len(overlay.Paths))
	}
	const want = "pve1 backs up to datastore main on PBS 10.50.0.9 via bond0 (10 Gbit/s). " +
		"1 backup job(s) (mon..fri 02:00) covering 2 guest(s). " +
		"Heuristic estimate from PVE backup-job and link config — validate the backup window against real dataset size and link utilization."
	if got := overlay.Paths[0].SizingHint; got != want {
		t.Errorf("sizing hint mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestProject_NoPBSStorageIsEmptyNotError proves the common case (a cluster
// with no PBS storage at all) yields an empty overlay, never an error or a
// spurious node — Project is a pure function, so an empty Status projects to
// an empty Overlay.
func TestProject_NoPBSStorageIsEmptyNotError(t *testing.T) {
	snap, _ := buildSnapshotAndStatus(t, fixtureBackupPaths)
	overlay := pbs.Project(snap, pbs.Status{Nodes: []string{"pve1", "pve2"}})
	if len(overlay.Hosts) != 0 || len(overlay.Paths) != 0 {
		t.Fatalf("empty Status must project to empty Overlay, got %d hosts / %d paths", len(overlay.Hosts), len(overlay.Paths))
	}
}
