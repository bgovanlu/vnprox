// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func sampleCapture(id, group, node string, startedAt int64) CaptureSession {
	return CaptureSession{
		ID: id, GroupID: group, TargetRef: "bridge:" + node + ":vmbr0", Node: node,
		Nodes:  []string{"pve1", "pve2"},
		Filter: "tcp port 443",
		Caps:   CaptureCaps{MaxDurationSec: 60, MaxBytes: 4096, MaxPackets: 100, RetentionHours: 24},
		Status: "running", StartedBy: "root@pam", StartedAt: startedAt,
		FilePath: "/var/lib/vnprox/captures/" + id + ".pcap",
	}
}

func TestCaptureRepo_UpsertGetByGroup(t *testing.T) {
	db := openTestDB(t)
	repo := NewCaptureRepo(db)
	ctx := context.Background()

	a := sampleCapture("s1", "g1", "pve1", 100)
	b := sampleCapture("s2", "g1", "pve2", 100)
	c := sampleCapture("s3", "g2", "pve1", 200)
	for _, s := range []CaptureSession{a, b, c} {
		if err := repo.Upsert(ctx, s); err != nil {
			t.Fatalf("Upsert %s: %v", s.ID, err)
		}
	}

	got, err := repo.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GroupID != "g1" || got.Node != "pve1" || got.Filter != "tcp port 443" {
		t.Errorf("Get(s1) = %+v", got)
	}
	if got.Caps.MaxPackets != 100 || len(got.Nodes) != 2 {
		t.Errorf("caps/nodes did not round-trip: %+v / %v", got.Caps, got.Nodes)
	}

	group, err := repo.ByGroup(ctx, "g1")
	if err != nil {
		t.Fatalf("ByGroup: %v", err)
	}
	if len(group) != 2 {
		t.Fatalf("ByGroup(g1) = %d rows, want 2", len(group))
	}

	// Upsert overwrites by id (status/accounting update).
	a.Status = "completed"
	a.Packets = 42
	a.FileBytes = 9000
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	got, _ = repo.Get(ctx, "s1")
	if got.Status != "completed" || got.Packets != 42 || got.FileBytes != 9000 {
		t.Errorf("update did not persist: %+v", got)
	}
}

func TestCaptureRepo_ListGroupsAndNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewCaptureRepo(db)
	ctx := context.Background()

	_ = repo.Upsert(ctx, sampleCapture("s1", "g1", "pve1", 100))
	_ = repo.Upsert(ctx, sampleCapture("s2", "g2", "pve1", 300))

	groups, err := repo.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 2 || groups[0] != "g2" { // newest first
		t.Errorf("ListGroups = %v, want [g2 g1]", groups)
	}

	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}
}
