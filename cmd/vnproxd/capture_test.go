package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/capturemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestCapture_AuditRowsGoldenAgainstStore is T-1301 AC6: every start/stop
// produces exactly one capture.start / capture.stop audit row with actor,
// target Ref, filter, and effective caps in detail — asserted against the
// same audit_log store GET /audit serves, exercising the real
// captureAuditAdapter + captureStoreAdapter production wiring.
func TestCapture_AuditRowsGoldenAgainstStore(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	auditRepo := store.NewAuditRepo(db)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	coord := capture.New(capture.Config{
		Ceilings:  capture.Caps{MaxDurationSec: 60, MaxBytes: 4096, MaxPackets: 100, RetentionHours: 24},
		Root:      t.TempDir(),
		Agent:     capturemock.NewAgent(),
		Resolver:  capture.RefResolver{},
		Store:     captureStoreAdapter{repo: store.NewCaptureRepo(db)},
		Audit:     captureAuditAdapter{repo: auditRepo},
		LocalNode: func() string { return "pve1" },
		Now:       func() time.Time { return now },
	})

	ctx := context.Background()
	g, err := coord.Start(ctx, capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "tcp port 443", StartedBy: "root@pam",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, stopErr := coord.StopGroup(ctx, g.ID, "root@pam"); stopErr != nil {
		t.Fatalf("StopGroup: %v", stopErr)
	}

	rows, err := auditRepo.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var starts, stops []store.AuditEntry
	for _, r := range rows {
		switch r.Action {
		case "capture.start":
			starts = append(starts, r)
		case "capture.stop":
			stops = append(stops, r)
		}
	}
	if len(starts) != 1 {
		t.Fatalf("capture.start audit rows = %d, want 1", len(starts))
	}
	if len(stops) != 1 {
		t.Fatalf("capture.stop audit rows = %d, want 1", len(stops))
	}

	start := starts[0]
	if start.Username != "root@pam" {
		t.Errorf("start actor = %q, want root@pam", start.Username)
	}
	if start.Target.String != "bridge:pve1:vmbr0" {
		t.Errorf("start target = %q, want bridge:pve1:vmbr0", start.Target.String)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(start.DetailJSON.String), &detail); err != nil {
		t.Fatalf("decoding start detail: %v", err)
	}
	if detail["filter"] != "tcp port 443" {
		t.Errorf("start detail filter = %v, want %q", detail["filter"], "tcp port 443")
	}
	for _, k := range []string{"maxDurationSec", "maxBytes", "maxPackets", "retentionHours"} {
		if _, ok := detail[k]; !ok {
			t.Errorf("start detail missing effective cap %q; got %v", k, detail)
		}
	}

	// AC7 corollary at the store boundary: no payload marker leaks into the
	// audit detail JSON.
	if strings.Contains(start.DetailJSON.String, "vnprox-udp") {
		t.Error("payload marker leaked into audit detail")
	}
}
