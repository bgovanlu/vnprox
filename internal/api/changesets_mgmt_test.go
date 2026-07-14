package api

// T-703 AC5's API-layer coverage: the touchesMgmtPath response flag is
// computed server-side over the same MgmtStatus computation the badges use,
// and the commit-confirm window floor for such a changeset is enforced
// server-side (400 confirm_window_too_short on a lower confirmTimeoutSec).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// mgmtStatusForPve1 is a fake MgmtStatusService reporting vmbr0 as pve1's
// mgmt carrier over bond0 -> eno1/eno2 (three-node-vlan's shape).
func mgmtStatusForPve1() fakeMgmtStatusService {
	ref := func(kind inventory.Kind, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: "pve1", ID: id}
	}
	return fakeMgmtStatusService{status: change.MgmtStatus{
		Source: "detected",
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{
				Ref:   ref(inventory.KindBridge, "vmbr0"),
				Roles: []topology.MgmtRole{topology.MgmtRoleMgmt},
				Path: []inventory.Ref{
					ref(inventory.KindBond, "bond0"),
					ref(inventory.KindPhysNic, "eno1"),
					ref(inventory.KindPhysNic, "eno2"),
				},
			}},
		},
	}}
}

func createChangeset(t *testing.T, r http.Handler, opsJSON string) changesetResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"t","ops":`+opsJSON+`}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var out changesetResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decoding create: %v", err)
	}
	return out
}

// TestChangesets_TouchesMgmtPathFlag_ComputedServerSide: a hand-built draft
// (no wizard) touching the mgmt path carries touchesMgmtPath=true on GET,
// while an unrelated one carries false.
func TestChangesets_TouchesMgmtPathFlag_ComputedServerSide(t *testing.T) {
	svc := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: svc, Protected: mgmtStatusForPve1(),
	})

	touching := createChangeset(t, r, `[{"op":"bond.update","target":"bond:pve1:bond0","params":{"slaves":["eno1","eno2","eno3"]}}]`)
	if !touching.TouchesMgmtPath {
		t.Errorf("touchesMgmtPath = false for a bond.update on the mgmt-path bond, want true")
	}

	unrelated := createChangeset(t, r, `[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"ports":["eno3"]}}]`)
	if unrelated.TouchesMgmtPath {
		t.Errorf("touchesMgmtPath = true for an unrelated bridge.create, want false")
	}

	// GET must recompute the same flag.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+touching.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got changesetResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding get: %v", err)
	}
	if !got.TouchesMgmtPath {
		t.Errorf("GET touchesMgmtPath = false, want true")
	}
}

// TestChangesets_ConfirmWindowFloor_EnforcedServerSide: applying a
// management-path changeset with a confirmTimeoutSec below the floor is
// rejected 400 confirm_window_too_short, before any apply work.
func TestChangesets_ConfirmWindowFloor_EnforcedServerSide(t *testing.T) {
	svc := newApplyConfiguredChangesetService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: svc, Protected: mgmtStatusForPve1(),
	})

	cs := createChangeset(t, r, `[{"op":"bond.update","target":"bond:pve1:bond0","params":{"slaves":["eno1","eno2","eno3"]}}]`)
	if !cs.TouchesMgmtPath {
		t.Fatalf("precondition: expected touchesMgmtPath=true")
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/apply",
		bytes.NewBufferString(`{"confirmTimeoutSec":60}`))
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusBadRequest {
		t.Fatalf("apply status = %d, want 400, body: %s", applyRec.Code, applyRec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(applyRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error: %v", err)
	}
	if errResp.Error.Code != "confirm_window_too_short" {
		t.Errorf("error code = %q, want confirm_window_too_short", errResp.Error.Code)
	}

	// The changeset must be untouched (still draft — the pre-check ran before
	// any apply work).
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+cs.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got changesetResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding get: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("status after rejected apply = %q, want draft", got.Status)
	}
}
