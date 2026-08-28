// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/automation"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestWebhookHealthAdapter_RaisesThenClearsUnhealthyFinding is T-1104's
// acceptance criterion 6: a webhook target with N consecutive failures
// raises webhook_unhealthy; recovery (a subsequent success) clears it —
// end to end against a real store.WebhookRepo (temp SQLite), not a fake,
// since the adapter's whole point is reading straight off that table live
// each cycle (adapt_webhook.go's doc comment: "no second persisted flag").
func TestWebhookHealthAdapter_RaisesThenClearsUnhealthyFinding(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/vnprox.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewWebhookRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, store.Webhook{ID: "wh1", URL: "https://example.com/hook", SecretEnc: []byte("x"), CreatedBy: "root@pam", CreatedAt: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a := webhookHealthAdapter{repo: repo, logger: testLogger()}
	if fs := a.Findings(); len(fs) != 0 {
		t.Fatalf("a freshly-registered webhook must not be unhealthy yet: %+v", fs)
	}

	for i := 0; i < automation.DefaultUnhealthyThreshold-1; i++ {
		if _, err := repo.RecordFailure(ctx, "wh1", int64(i+1), "boom"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	if fs := a.Findings(); len(fs) != 0 {
		t.Fatalf("below the unhealthy threshold, no finding should fire yet: %+v", fs)
	}

	if _, err := repo.RecordFailure(ctx, "wh1", 100, "still failing"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	fs := a.Findings()
	if len(fs) != 1 {
		t.Fatalf("Findings() after reaching the threshold = %+v, want exactly 1", fs)
	}
	if fs[0].Check != "webhook_unhealthy" || fs[0].Source != findings.SourceHealth {
		t.Errorf("finding = %+v, want check=webhook_unhealthy source=health", fs[0])
	}
	if fs[0].ID != "health:webhook_unhealthy|wh1" {
		t.Errorf("finding ID = %q, want a stable health:webhook_unhealthy|<id> key", fs[0].ID)
	}

	// Recovery: a subsequent success resets the counter to 0, clearing the
	// finding on the next cycle (this call).
	if err := repo.RecordSuccess(ctx, "wh1", 200); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if fs := a.Findings(); len(fs) != 0 {
		t.Fatalf("Findings() after recovery = %+v, want none", fs)
	}
}

func TestWebhookHealthAdapter_ListErrorIsSafe(t *testing.T) {
	a := webhookHealthAdapter{repo: erroringWebhookStore{}, logger: testLogger()}
	if fs := a.Findings(); fs != nil {
		t.Errorf("a listing error must contribute no findings (logged, not panicked), got %+v", fs)
	}
}

type erroringWebhookStore struct{}

func (erroringWebhookStore) List(context.Context) ([]store.Webhook, error) {
	return nil, context.DeadlineExceeded
}
