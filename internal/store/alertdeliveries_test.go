// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
)

func TestAlertDeliveryRepo_InsertList(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertDeliveryRepo(db)
	ctx := context.Background()

	rows := []AlertDelivery{
		{ID: "d1", RuleID: "r1", FindingID: "f1", At: 100, Attempt: 1, Status: "retrying", Error: "http 500"},
		{ID: "d2", RuleID: "r1", FindingID: "f1", At: 110, Attempt: 2, Status: "delivered"},
		{ID: "d3", RuleID: "r2", FindingID: "f2", At: 120, Attempt: 1, Status: "failed", Error: "timeout"},
	}
	for _, d := range rows {
		if err := repo.Insert(ctx, d); err != nil {
			t.Fatalf("Insert(%s): %v", d.ID, err)
		}
	}

	all, err := repo.List(ctx, "", "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(all) len = %d, want 3", len(all))
	}
	// Newest-first.
	if all[0].ID != "d3" || all[1].ID != "d2" || all[2].ID != "d1" {
		t.Errorf("List(all) order = [%s,%s,%s], want [d3,d2,d1]", all[0].ID, all[1].ID, all[2].ID)
	}

	byRule, err := repo.List(ctx, "r1", "")
	if err != nil {
		t.Fatalf("List(r1): %v", err)
	}
	if len(byRule) != 2 {
		t.Fatalf("List(r1) len = %d, want 2", len(byRule))
	}

	byStatus, err := repo.List(ctx, "", "delivered")
	if err != nil {
		t.Fatalf("List(delivered): %v", err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != "d2" {
		t.Fatalf("List(delivered) = %+v, want [d2]", byStatus)
	}

	byBoth, err := repo.List(ctx, "r1", "retrying")
	if err != nil {
		t.Fatalf("List(r1, retrying): %v", err)
	}
	if len(byBoth) != 1 || byBoth[0].ID != "d1" {
		t.Fatalf("List(r1, retrying) = %+v, want [d1]", byBoth)
	}

	if got, err := repo.List(ctx, "no-such-rule", ""); err != nil || len(got) != 0 {
		t.Errorf("List(no-such-rule) = %+v, %v, want empty, nil", got, err)
	}
}

func TestAlertDeliveryRepo_ErrorColumnRoundtrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertDeliveryRepo(db)
	ctx := context.Background()

	if err := repo.Insert(ctx, AlertDelivery{ID: "d1", RuleID: "r1", FindingID: "f1", At: 1, Attempt: 1, Status: "delivered"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.List(ctx, "r1", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Error != "" {
		t.Fatalf("List() = %+v, want one row with empty Error", got)
	}
}
