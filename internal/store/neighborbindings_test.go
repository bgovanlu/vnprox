// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"testing"
)

func sampleBinding(at int64, node, ip, mac, prevMac string) NeighborBinding {
	b := NeighborBinding{At: at, Node: node, IP: ip, MAC: mac, Iface: "vmbr0", State: "REACHABLE"}
	if prevMac != "" {
		b.PrevMAC = sql.NullString{String: prevMac, Valid: true}
	}
	return b
}

func TestNeighborBindingRepo_InsertAndQuery(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	rows := []NeighborBinding{
		sampleBinding(100, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:01", ""),
		sampleBinding(200, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:02", "aa:aa:aa:aa:aa:01"),
		sampleBinding(300, "pve2", "10.0.1.1", "aa:aa:aa:aa:aa:03", ""),
	}
	for _, b := range rows {
		if err := repo.Insert(ctx, b); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	items, next, err := repo.Query(ctx, NeighborBindingFilter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if next != "" {
		t.Fatalf("expected no next cursor, got %q", next)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// Newest first.
	if items[0].At != 300 || items[1].At != 200 || items[2].At != 100 {
		t.Fatalf("unexpected order: %+v", items)
	}
	if items[1].PrevMAC.String != "aa:aa:aa:aa:aa:01" || !items[1].PrevMAC.Valid {
		t.Fatalf("expected prev_mac to round-trip, got %+v", items[1].PrevMAC)
	}
	if items[2].PrevMAC.Valid {
		t.Fatalf("expected first-seen row to have NULL prev_mac, got %+v", items[2].PrevMAC)
	}
}

func TestNeighborBindingRepo_Query_Filters(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for _, b := range []NeighborBinding{
		sampleBinding(100, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:01", ""),
		sampleBinding(200, "pve1", "10.0.0.2", "aa:aa:aa:aa:aa:02", ""),
		sampleBinding(300, "pve2", "10.0.1.1", "aa:aa:aa:aa:aa:01", ""),
	} {
		if err := repo.Insert(ctx, b); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	items, _, err := repo.Query(ctx, NeighborBindingFilter{Node: "pve1"}, "", 10)
	if err != nil {
		t.Fatalf("Query(node): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Query(node=pve1) got %d items, want 2", len(items))
	}

	items, _, err = repo.Query(ctx, NeighborBindingFilter{IP: "10.0.0.1"}, "", 10)
	if err != nil {
		t.Fatalf("Query(ip): %v", err)
	}
	if len(items) != 1 || items[0].IP != "10.0.0.1" {
		t.Fatalf("Query(ip=10.0.0.1) = %+v, want exactly the one matching row", items)
	}

	items, _, err = repo.Query(ctx, NeighborBindingFilter{MAC: "aa:aa:aa:aa:aa:01"}, "", 10)
	if err != nil {
		t.Fatalf("Query(mac): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Query(mac) got %d items, want 2", len(items))
	}
}

func TestNeighborBindingRepo_Query_Pagination(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for i := int64(0); i < 5; i++ {
		if err := repo.Insert(ctx, sampleBinding(100+i, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:0"+string(rune('0'+i)), "")); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	page1, cursor1, err := repo.Query(ctx, NeighborBindingFilter{}, "", 2)
	if err != nil {
		t.Fatalf("Query page1: %v", err)
	}
	if len(page1) != 2 || cursor1 == "" {
		t.Fatalf("page1 = %+v, cursor = %q, want 2 items and a cursor", page1, cursor1)
	}
	if page1[0].At != 104 || page1[1].At != 103 {
		t.Fatalf("page1 order = %+v", page1)
	}

	page2, cursor2, err := repo.Query(ctx, NeighborBindingFilter{}, cursor1, 2)
	if err != nil {
		t.Fatalf("Query page2: %v", err)
	}
	if len(page2) != 2 || cursor2 == "" {
		t.Fatalf("page2 = %+v, cursor = %q, want 2 items and a cursor", page2, cursor2)
	}
	if page2[0].At != 102 || page2[1].At != 101 {
		t.Fatalf("page2 order = %+v", page2)
	}

	page3, cursor3, err := repo.Query(ctx, NeighborBindingFilter{}, cursor2, 2)
	if err != nil {
		t.Fatalf("Query page3: %v", err)
	}
	if len(page3) != 1 || cursor3 != "" {
		t.Fatalf("page3 = %+v, cursor = %q, want 1 item and no further cursor", page3, cursor3)
	}
	if page3[0].At != 100 {
		t.Fatalf("page3 order = %+v", page3)
	}
}

func TestNeighborBindingRepo_LatestByIP(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for _, b := range []NeighborBinding{
		sampleBinding(100, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:01", ""),
		sampleBinding(200, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:02", "aa:aa:aa:aa:aa:01"),
		sampleBinding(150, "pve1", "10.0.0.2", "aa:aa:aa:aa:aa:03", ""),
		sampleBinding(300, "pve2", "10.0.0.1", "aa:aa:aa:aa:aa:99", ""), // different node, must not collide
	} {
		if err := repo.Insert(ctx, b); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	latest, err := repo.LatestByIP(ctx, "pve1")
	if err != nil {
		t.Fatalf("LatestByIP: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("LatestByIP got %d entries, want 2: %+v", len(latest), latest)
	}
	if latest["10.0.0.1"].MAC != "aa:aa:aa:aa:aa:02" {
		t.Fatalf("LatestByIP[10.0.0.1].MAC = %q, want the newer MAC", latest["10.0.0.1"].MAC)
	}
	if latest["10.0.0.2"].MAC != "aa:aa:aa:aa:aa:03" {
		t.Fatalf("LatestByIP[10.0.0.2].MAC = %q", latest["10.0.0.2"].MAC)
	}
}

func TestNeighborBindingRepo_CountSince(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for _, b := range []NeighborBinding{
		sampleBinding(100, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:01", ""),
		sampleBinding(200, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:02", "aa:aa:aa:aa:aa:01"),
		sampleBinding(300, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:03", "aa:aa:aa:aa:aa:02"),
	} {
		if err := repo.Insert(ctx, b); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	n, err := repo.CountSince(ctx, "pve1", "10.0.0.1", "", 150)
	if err != nil {
		t.Fatalf("CountSince: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountSince(since=150) = %d, want 2 (the 200 and 300 rows)", n)
	}

	n, err = repo.CountSince(ctx, "pve1", "10.0.0.1", "", 0)
	if err != nil {
		t.Fatalf("CountSince: %v", err)
	}
	// The very first row (100) has a NULL prev_mac (first-ever sighting,
	// not a rebind) and is deliberately excluded — only the two genuine
	// transitions (200, 300) count.
	if n != 2 {
		t.Fatalf("CountSince(since=0) = %d, want 2 (first-seen row excluded)", n)
	}
}

func TestNeighborBindingRepo_DistinctIPsSince_And_CandidateMACsSince(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	mac := "bb:bb:bb:bb:bb:01"
	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if err := repo.Insert(ctx, sampleBinding(int64(100+i), "pve1", ip, mac, "")); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	// A different, unrelated binding that should not appear as a candidate
	// for this MAC's IP set.
	if err := repo.Insert(ctx, sampleBinding(400, "pve1", "10.0.0.9", "cc:cc:cc:cc:cc:01", "")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	ips, err := repo.DistinctIPsSince(ctx, "pve1", mac, 0)
	if err != nil {
		t.Fatalf("DistinctIPsSince: %v", err)
	}
	if len(ips) != 3 {
		t.Fatalf("DistinctIPsSince = %+v, want 3 IPs", ips)
	}

	macs, err := repo.CandidateMACsSince(ctx, "pve1", 0)
	if err != nil {
		t.Fatalf("CandidateMACsSince: %v", err)
	}
	if len(macs) != 2 {
		t.Fatalf("CandidateMACsSince = %+v, want 2 distinct MACs", macs)
	}
}

func TestNeighborBindingRepo_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for _, b := range []NeighborBinding{
		sampleBinding(100, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:01", ""),
		sampleBinding(200, "pve1", "10.0.0.2", "aa:aa:aa:aa:aa:02", ""),
		sampleBinding(300, "pve1", "10.0.0.3", "aa:aa:aa:aa:aa:03", ""),
	} {
		if err := repo.Insert(ctx, b); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	n, err := repo.PruneOlderThan(ctx, 200)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneOlderThan removed %d rows, want 1", n)
	}

	items, _, err := repo.Query(ctx, NeighborBindingFilter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items after prune, want 2", len(items))
	}
}

func TestNeighborBindingRepo_PruneToCap(t *testing.T) {
	db := openTestDB(t)
	repo := NewNeighborBindingRepo(db)
	ctx := context.Background()

	for i := int64(0); i < 5; i++ {
		if err := repo.Insert(ctx, sampleBinding(100+i, "pve1", "10.0.0.1", "aa:aa:aa:aa:aa:0"+string(rune('0'+i)), "")); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	n, err := repo.PruneToCap(ctx, 3)
	if err != nil {
		t.Fatalf("PruneToCap: %v", err)
	}
	if n != 2 {
		t.Fatalf("PruneToCap removed %d rows, want 2", n)
	}

	items, _, err := repo.Query(ctx, NeighborBindingFilter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items after PruneToCap, want 3", len(items))
	}
	// The newest 3 must survive.
	if items[0].At != 104 || items[1].At != 103 || items[2].At != 102 {
		t.Fatalf("unexpected survivors after PruneToCap: %+v", items)
	}

	// maxRows <= 0 is a documented no-op, never "delete everything".
	n, err = repo.PruneToCap(ctx, 0)
	if err != nil {
		t.Fatalf("PruneToCap(0): %v", err)
	}
	if n != 0 {
		t.Fatalf("PruneToCap(0) removed %d rows, want 0", n)
	}
}
