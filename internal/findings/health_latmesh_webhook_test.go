package findings

// AC6 ("Findings route through T-1005: a fixture-triggered path_loss
// finding produces a delivery via a test alert rule, extends T-1005's
// webhook receiver test"): reuses webhook_test.go's own fixtures
// (fakeRuleProvider, fakeRecorder) and delivery entry point
// (WebhookNotifier.Notify) verbatim — proving a path_loss finding flows
// through the existing T-1005 delivery pipeline unmodified, exactly like
// every other producer's findings already do (bond_slave_down in
// webhook_test.go's own testFinding helper). No new delivery logic is
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

// fixedLatMeshProvider always reports the same over-threshold link — enough
// consecutive breaches (latMeshRiseCycles) drive checkPathLoss to a real,
// fired Finding this test then hands to WebhookNotifier.
type fixedLatMeshProvider struct {
	link latmesh.LinkHeat
}

func (p fixedLatMeshProvider) LatMeshHeatmap() ([]latmesh.LinkHeat, error) {
	return []latmesh.LinkHeat{p.link}, nil
}

func TestPathLoss_DeliversViaAlertRule(t *testing.T) {
	prov := fixedLatMeshProvider{link: latmesh.LinkHeat{
		LinkID: "guest:vmbr0|pve1->pve2", Fabric: latmesh.FabricGuest,
		FromNode: "pve1", ToNode: "pve2",
		RollingLossPct: 9.5, RollingRttMs: 5,
	}}
	db := newDebouncer()
	th := DefaultThresholds

	var found []Finding
	for i := 0; i < latMeshRiseCycles; i++ {
		found = checkPathLoss(prov, db, th)
	}
	if len(found) != 1 {
		t.Fatalf("got %d path_loss findings after %d cycles, want 1: %+v", len(found), latMeshRiseCycles, found)
	}
	f := found[0]
	if f.Check != CheckPathLoss || f.Source != SourceHealth {
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

	rule := AlertRule{ID: "r-latmesh", Enabled: true, TargetKind: TargetGeneric, TargetURL: srv.URL}
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
		if got.ID != f.ID || got.Check != CheckPathLoss {
			t.Errorf("delivered finding = %+v, want id=%s check=%s", got, f.ID, CheckPathLoss)
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
