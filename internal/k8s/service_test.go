package k8s_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
)

func TestService_Poll_CachesOverlayAndFindings(t *testing.T) {
	f := loadClusterFixture(t, "cluster-flannel.yaml")
	srv, _ := k8smock.NewServer(f)
	defer srv.Close()

	client := &k8s.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
	index := k8s.GuestIPIndex(func(ip string) (string, bool) {
		if ip == "10.10.0.11" {
			return "guest:pve1:105", true
		}
		return "", false
	})
	lookup := k8s.FwLookup(func(string) (guest, cluster *inventory.FwRuleset) { return nil, nil })

	svc := k8s.NewPoller()
	fixedNow := func() time.Time { return time.Unix(1000, 0) }

	overlay, findings, err := svc.Poll(context.Background(), "c1", client, index, lookup, fixedNow)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if overlay.GeneratedAt != 1000 {
		t.Errorf("GeneratedAt = %d, want 1000", overlay.GeneratedAt)
	}
	// The flannel fixture's NodePort service (30080) has no covering
	// firewall rule (lookup always returns nil,nil), and node1 matches —
	// expect exactly one finding.
	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1", len(findings))
	}

	last, ok := svc.Last("c1")
	if !ok {
		t.Fatal("Last(c1) not found after Poll")
	}
	if last.Err != "" {
		t.Errorf("last.Err = %q, want empty", last.Err)
	}
	if len(last.Findings) != 1 {
		t.Errorf("cached findings = %+v, want 1", last.Findings)
	}

	cached := svc.CachedFindings()
	if len(cached) != 1 {
		t.Fatalf("CachedFindings = %+v, want 1", cached)
	}

	svc.Forget("c1")
	if _, ok := svc.Last("c1"); ok {
		t.Error("Last(c1) should be gone after Forget")
	}
	if len(svc.CachedFindings()) != 0 {
		t.Error("CachedFindings should be empty after Forget")
	}
}

func TestService_Poll_CachesError(t *testing.T) {
	// A client pointed at a server that returns nothing usable (closed
	// immediately) should cache an error, not panic or silently succeed.
	srv, _ := k8smock.NewServer(k8smock.Fixture{})
	srv.Close() // close immediately so every request fails

	client := &k8s.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
	svc := k8s.NewPoller()

	_, _, err := svc.Poll(context.Background(), "c1", client, nil, nil, nil)
	if err == nil {
		t.Fatal("Poll against a closed server should return an error")
	}
	last, ok := svc.Last("c1")
	if !ok || last.Err == "" {
		t.Errorf("Last(c1) = %+v, %v, want a cached error", last, ok)
	}
}
