// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeExtStore is an in-memory ipam.ExternalSubnetStore for the route tests.
type fakeExtStore struct {
	rows map[string]store.ExternalSubnet
}

func newFakeExtStore() *fakeExtStore { return &fakeExtStore{rows: map[string]store.ExternalSubnet{}} }

func (f *fakeExtStore) Insert(_ context.Context, e store.ExternalSubnet) error {
	for _, r := range f.rows {
		if r.CIDR == e.CIDR {
			return errors.New("duplicate cidr")
		}
	}
	f.rows[e.ID] = e
	return nil
}
func (f *fakeExtStore) Get(_ context.Context, id string) (store.ExternalSubnet, error) {
	r, ok := f.rows[id]
	if !ok {
		return store.ExternalSubnet{}, store.ErrNotFound
	}
	return r, nil
}
func (f *fakeExtStore) List(_ context.Context) ([]store.ExternalSubnet, error) {
	out := make([]store.ExternalSubnet, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out, nil
}
func (f *fakeExtStore) Update(_ context.Context, e store.ExternalSubnet) error {
	if _, ok := f.rows[e.ID]; !ok {
		return store.ErrNotFound
	}
	f.rows[e.ID] = e
	return nil
}
func (f *fakeExtStore) Delete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

func newExternalIPAMRouter(t *testing.T, ext ipam.ExternalSubnetStore) http.Handler {
	t.Helper()
	svc := ipam.NewService(ipam.Config{External: ext})
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:         fakeAuthWithUser{username: "admin@pam", fakeAuth: fakeAuth{authenticated: true}},
		IPAMExternal: svc,
	})
}

func TestExternalSubnetRoutes_CRUD(t *testing.T) {
	r := newExternalIPAMRouter(t, newFakeExtStore())

	// POST create.
	body, _ := json.Marshal(map[string]string{"cidr": "203.0.113.0/24", "label": "colo", "source": "manual"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ipam/external-subnets", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var created ipam.ExternalSubnet
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.CIDR != "203.0.113.0/24" || created.Source != "manual" {
		t.Fatalf("created = %+v", created)
	}

	// GET list.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ipam/external-subnets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d", rec.Code)
	}
	var list struct {
		Items []ipam.ExternalSubnet `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Fatalf("list has %d items, want 1", len(list.Items))
	}

	// DELETE.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/ipam/external-subnets/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}
}

func TestExternalSubnetRoutes_CreateValidation(t *testing.T) {
	r := newExternalIPAMRouter(t, newFakeExtStore())
	body, _ := json.Marshal(map[string]string{"cidr": "not-a-cidr"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ipam/external-subnets", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST malformed cidr status = %d, want 400", rec.Code)
	}
}

// fakeFedIPAM is a FederationIPAMSource test double.
type fakeFedIPAM struct {
	clusters []ipam.ClusterSubnets
	failed   []string
	partial  bool
}

func (f fakeFedIPAM) IPAMSubnets(context.Context) ([]ipam.ClusterSubnets, bool, []string, error) {
	return f.clusters, f.partial, f.failed, nil
}

// TestFederationIPAMConflictsRoute is T-1203 AC2 at the HTTP layer: two
// clusters sharing a CIDR return one cross_cluster_duplicate_subnet finding.
func TestFederationIPAMConflictsRoute(t *testing.T) {
	src := fakeFedIPAM{clusters: []ipam.ClusterSubnets{
		{ClusterID: "a", ClusterName: "east", CIDRs: []string{"10.10.0.0/24"}},
		{ClusterID: "b", ClusterName: "west", CIDRs: []string{"10.10.0.0/24"}},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:           fakeAuthWithUser{username: "admin@pam", fakeAuth: fakeAuth{authenticated: true}},
		FederationIPAM: src,
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/federation/ipam/conflicts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conflicts status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items          []ipam.Conflict `json:"items"`
		FailedClusters []string        `json:"failedClusters"`
		Partial        bool            `json:"partial"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Type != ipam.ConflictCrossClusterDuplicateSubnet {
		t.Errorf("conflict type = %q", resp.Items[0].Type)
	}
	if len(resp.Items[0].Clusters) != 2 {
		t.Errorf("conflict clusters = %v, want 2", resp.Items[0].Clusters)
	}
}
