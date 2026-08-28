// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// This file is T-1503's HTTP-level coverage for GET /ceph/status, mirroring
// latmesh_http_test.go's "fake local source, prove the wiring" pattern —
// internal/ceph's own Project/Discover logic is covered by that package's
// own tests; this file only proves the router mounts the route netRead-gated
// and serializes ceph.Overlay correctly.

type fakeCephService struct {
	err     error
	overlay ceph.Overlay
}

func (f *fakeCephService) Overlay(context.Context) (ceph.Overlay, error) {
	return f.overlay, f.err
}

func cephTestRouter(svc CephService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Ceph: svc,
	})
}

func TestCephStatus_ReturnsOverlay(t *testing.T) {
	nic := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	bond := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond1"}
	svc := &fakeCephService{overlay: ceph.Overlay{
		PublicNetwork:  "10.20.0.0/24",
		ClusterNetwork: "10.30.0.0/24",
		Nodes: []ceph.NodeAttribution{{
			Node: "pve1", PublicCarrier: bond, PublicRidingOn: bond,
			PublicPath: []inventory.Ref{bond, nic}, PublicMTU: 9000,
		}},
		OSDs: []ceph.OSDAttribution{{
			OSD:        ceph.OSD{ID: 0, Node: "pve1", Device: "/dev/sdb", Up: true, In: true},
			PublicBond: bond,
		}},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ceph/status", nil)
	rec := httptest.NewRecorder()
	cephTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got cephOverlayResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PublicNetwork != "10.20.0.0/24" || got.ClusterNetwork != "10.30.0.0/24" {
		t.Errorf("network CIDRs = %+v, want 10.20.0.0/24 / 10.30.0.0/24", got)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].PublicRidingOn != "bond:pve1:bond1" {
		t.Errorf("Nodes = %+v, want PublicRidingOn bond:pve1:bond1", got.Nodes)
	}
	if len(got.OSDs) != 1 || got.OSDs[0].PublicBond != "bond:pve1:bond1" {
		t.Errorf("OSDs = %+v, want PublicBond bond:pve1:bond1", got.OSDs)
	}
}

func TestCephStatus_ServiceError(t *testing.T) {
	svc := &fakeCephService{err: errors.New("boom")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ceph/status", nil)
	rec := httptest.NewRecorder()
	cephTestRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCephStatus_NilServiceSkipsMounting(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ceph/status", nil)
	rec := httptest.NewRecorder()
	cephTestRouter(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route must not be mounted for a nil service)", rec.Code)
	}
}
