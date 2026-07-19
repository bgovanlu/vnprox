package findings

// T-1405 AC3 ("A wan_degraded finding routed through an alert_rules entry
// (T-1005) delivers one signed webhook to a mock target — reuses T-1104's
// webhook-delivery test pattern"): reuses webhook_test.go's own fixtures
// (fakeRuleProvider, fakeRecorder) and delivery entry point
// (WebhookNotifier.Notify) verbatim — the identical shape
// TestPathLoss_DeliversViaAlertRule (health_latmesh_webhook_test.go)
// already proves for T-1303's own new check. No new delivery logic is
// added by this task; this test is the "and it works for our new check
// too" regression T-1005's own design already promised.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// fixedWanProvider always reports the same over-threshold link — enough
// consecutive breaches (wanRiseCycles) drive checkWanDegraded to a real,
// fired Finding this test then hands to WebhookNotifier.
type fixedWanProvider struct {
	link latmesh.LinkHeat
}

func (p fixedWanProvider) WanHeatmap() ([]latmesh.LinkHeat, error) {
	return []latmesh.LinkHeat{p.link}, nil
}

func TestWanDegraded_DeliversViaAlertRule(t *testing.T) {
	prov := fixedWanProvider{link: latmesh.LinkHeat{
		LinkID:         latmesh.ComputeLinkID("wan", "vmbr0", "pve1", "1.1.1.1"),
		Fabric:         "wan",
		FromNode:       "pve1",
		ToNode:         "1.1.1.1",
		RollingLossPct: 55,
		RollingRttMs:   30,
	}}
	db := newDebouncer()
	th := DefaultThresholds

	var found []Finding
	for i := 0; i < wanRiseCycles; i++ {
		found = checkWanDegraded(prov, db, th)
	}
	if len(found) != 1 {
		t.Fatalf("got %d wan_degraded findings after %d cycles, want 1: %+v", len(found), wanRiseCycles, found)
	}
	f := found[0]
	if f.Check != CheckWanDegraded || f.Source != SourceWan {
		t.Fatalf("unexpected finding shape: %+v", f)
	}

	// Now deliver it through the exact same webhook path
	// TestWebhookNotifier_RoutingFilters exercises for every other check.
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := AlertRule{ID: "r-wan", Enabled: true, TargetKind: TargetGeneric, TargetURL: srv.URL}
	rec := &fakeRecorder{}
	n := NewWebhookNotifier(WebhookNotifierConfig{
		Rules:    fakeRuleProvider{rules: []AlertRule{rule}},
		Recorder: rec,
		Client:   srv.Client(),
		Sleep:    func(context.Context, time.Duration) {},
	})

	if err := n.Notify(context.Background(), f, TransitionNew); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	select {
	case body := <-received:
		var got Finding
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("Unmarshal delivered body: %v", err)
		}
		if got.ID != f.ID || got.Check != CheckWanDegraded {
			t.Errorf("delivered finding = %+v, want id=%s check=%s", got, f.ID, CheckWanDegraded)
		}
	default:
		t.Fatal("webhook target never received a delivery")
	}

	deliveries := rec.snapshot()
	if len(deliveries) == 0 {
		t.Fatal("no delivery recorded")
	}
	if deliveries[0].Status != "delivered" {
		t.Errorf("delivery status = %q, want delivered", deliveries[0].Status)
	}
}
