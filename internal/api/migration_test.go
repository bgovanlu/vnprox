package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/migration"
)

// buildMigrationGraph builds a two-node graph with a 1000Mbps vmbr0 on each
// node and one qemu guest (vmid 100) on pve1 — enough to exercise POST
// /migration/preflight end to end without a real PVE/latmesh dependency.
func buildMigrationGraph(t *testing.T) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve2", ID: "pve2"}, Name: "pve2", Status: "online"},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, VMID: 100, Node: "pve1", Name: "vm100", Type: "qemu", Status: "running"},
	})
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve2", ID: "eno1"}, Name: "eno1", SpeedMbps: 1000, LinkUp: true},
	})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux, DeclaredPortNames: []string{"eno1"}},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux, DeclaredPortNames: []string{"eno1"}},
	})
	return g
}

func postMigrationPreflight(t *testing.T, planner *migration.Planner, auth AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Migration: planner,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/migration/preflight", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMigrationPreflight_Unauthenticated401(t *testing.T) {
	g := buildMigrationGraph(t)
	planner := migration.New(migration.Config{Graph: g})
	rec := postMigrationPreflight(t, planner, fakeAuth{authenticated: false}, `{"guest":"guest:pve1:100","targetNode":"pve2"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMigrationPreflight_OK(t *testing.T) {
	g := buildMigrationGraph(t)
	planner := migration.New(migration.Config{Graph: g})
	rec := postMigrationPreflight(t, planner, netReadAuth(), `{"guest":"guest:pve1:100","targetNode":"pve2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got migration.Assessment
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Verdict == "" {
		t.Error("expected a non-empty verdict")
	}
	if !got.BestEffort {
		t.Error("expected bestEffort=true")
	}
	if got.Caveats == nil {
		t.Error("expected Caveats to be a non-nil (possibly empty) slice")
	}
	// The wire shape must be exactly the pinned five fields — no more, no
	// less (docs/api.md's Migration planner section).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	wantKeys := []string{"headroomMbps", "estimatedTransferSec", "verdict", "bestEffort", "caveats"}
	if len(raw) != len(wantKeys) {
		t.Errorf("response has %d keys, want %d: %v", len(raw), len(wantKeys), raw)
	}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("response missing key %q", k)
		}
	}
}

func TestMigrationPreflight_MissingTargetNode400(t *testing.T) {
	g := buildMigrationGraph(t)
	planner := migration.New(migration.Config{Graph: g})
	rec := postMigrationPreflight(t, planner, netReadAuth(), `{"guest":"guest:pve1:100"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMigrationPreflight_BadGuestRef400(t *testing.T) {
	g := buildMigrationGraph(t)
	planner := migration.New(migration.Config{Graph: g})
	rec := postMigrationPreflight(t, planner, netReadAuth(), `{"guest":"not-a-ref","targetNode":"pve2"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMigrationPreflight_NonGuestRefKind400(t *testing.T) {
	g := buildMigrationGraph(t)
	planner := migration.New(migration.Config{Graph: g})
	rec := postMigrationPreflight(t, planner, netReadAuth(), `{"guest":"bridge:pve1:vmbr0","targetNode":"pve2"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMigrationPreflight_NilPlannerNotMounted(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger(), Auth: netReadAuth()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/migration/preflight", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted with a nil planner)", rec.Code)
	}
}
