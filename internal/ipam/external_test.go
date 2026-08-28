// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeExternalStore is an in-memory ipam.ExternalSubnetStore for the CRUD and
// subnet-list tests — the same "small in-memory double" pattern the ipam
// tests already use for inventory, avoiding a real SQLite dependency in this
// black-box test package. Keyed by id; a duplicate CIDR is rejected to mirror
// the store's unique index.
type fakeExternalStore struct {
	rows map[string]store.ExternalSubnet
}

func newFakeExternalStore() *fakeExternalStore {
	return &fakeExternalStore{rows: map[string]store.ExternalSubnet{}}
}

func (f *fakeExternalStore) Insert(_ context.Context, e store.ExternalSubnet) error {
	for _, r := range f.rows {
		if r.CIDR == e.CIDR {
			return errors.New("duplicate cidr")
		}
	}
	f.rows[e.ID] = e
	return nil
}

func (f *fakeExternalStore) Get(_ context.Context, id string) (store.ExternalSubnet, error) {
	r, ok := f.rows[id]
	if !ok {
		return store.ExternalSubnet{}, store.ErrNotFound
	}
	return r, nil
}

func (f *fakeExternalStore) List(_ context.Context) ([]store.ExternalSubnet, error) {
	out := make([]store.ExternalSubnet, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out, nil
}

func (f *fakeExternalStore) Update(_ context.Context, e store.ExternalSubnet) error {
	if _, ok := f.rows[e.ID]; !ok {
		return store.ErrNotFound
	}
	f.rows[e.ID] = e
	return nil
}

func (f *fakeExternalStore) Delete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

// newIpamServiceWithExternal builds a Service over the ipam-lab fixture with
// an external-subnet store wired.
func newIpamServiceWithExternal(t *testing.T, ext ipam.ExternalSubnetStore) *ipam.Service {
	t.Helper()
	f, err := pvemock.LoadFixture(ipamLabFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	ts := httptest.NewServer(pvemock.NewServer(f))
	t.Cleanup(ts.Close)
	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return ipam.NewService(ipam.Config{PVE: client, Inventory: ipamLabInventory(), External: ext})
}

// TestExternalSubnetCRUD_ShowsInSubnets is T-1203 AC1: external subnet CRUD
// produces source "external" rows in GET /ipam/subnets alongside sdn/bridge
// rows.
func TestExternalSubnetCRUD_ShowsInSubnets(t *testing.T) {
	ext := newFakeExternalStore()
	svc := newIpamServiceWithExternal(t, ext)
	ctx := context.Background()

	created, err := svc.CreateExternalSubnet(ctx, "203.0.113.0/24", "colo-lan", "manual", "physical colo LAN", "admin@pam")
	if err != nil {
		t.Fatalf("CreateExternalSubnet: %v", err)
	}
	if created.Source != "manual" || created.CIDR != "203.0.113.0/24" {
		t.Fatalf("created = %+v, want source manual cidr 203.0.113.0/24", created)
	}

	resp, err := svc.Subnets(ctx)
	if err != nil {
		t.Fatalf("Subnets: %v", err)
	}
	bySource := map[string][]string{}
	for _, s := range resp.Items {
		bySource[s.Source] = append(bySource[s.Source], s.CIDR)
	}
	// All three sources coexist in one list.
	if len(bySource["sdn"]) == 0 {
		t.Errorf("no sdn rows in subnet list: %+v", bySource)
	}
	if len(bySource["bridge"]) == 0 {
		t.Errorf("no bridge rows in subnet list: %+v", bySource)
	}
	if got := bySource["external"]; len(got) != 1 || got[0] != "203.0.113.0/24" {
		t.Fatalf("external rows = %v, want [203.0.113.0/24]", got)
	}
	// The external row is read-only (a pure record, not reserve/release-able).
	for _, s := range resp.Items {
		if s.Source == "external" && !s.ReadOnly {
			t.Errorf("external subnet %s not marked readOnly", s.CIDR)
		}
	}

	// Update, then delete, and confirm the row leaves the list.
	if _, err = svc.UpdateExternalSubnet(ctx, created.ID, "203.0.113.0/25", "colo-lan-2", "netbox", "narrowed"); err != nil {
		t.Fatalf("UpdateExternalSubnet: %v", err)
	}
	if err = svc.DeleteExternalSubnet(ctx, created.ID); err != nil {
		t.Fatalf("DeleteExternalSubnet: %v", err)
	}
	resp, err = svc.Subnets(ctx)
	if err != nil {
		t.Fatalf("Subnets after delete: %v", err)
	}
	for _, s := range resp.Items {
		if s.Source == "external" {
			t.Errorf("external row %s still present after delete", s.CIDR)
		}
	}
}

// TestExternalSubnet_Validation rejects a malformed CIDR and an unknown
// source, and canonicalizes a non-network CIDR to its network form.
func TestExternalSubnet_Validation(t *testing.T) {
	ext := newFakeExternalStore()
	svc := newIpamServiceWithExternal(t, ext)
	ctx := context.Background()

	if _, err := svc.CreateExternalSubnet(ctx, "not-a-cidr", "", "manual", "", "u"); err == nil {
		t.Error("CreateExternalSubnet accepted a malformed CIDR")
	}
	if _, err := svc.CreateExternalSubnet(ctx, "10.0.0.0/8", "", "bogus", "", "u"); err == nil {
		t.Error("CreateExternalSubnet accepted an unknown source")
	}
	// A host address is canonicalized to its network.
	got, err := svc.CreateExternalSubnet(ctx, "192.0.2.55/24", "", "", "", "u")
	if err != nil {
		t.Fatalf("CreateExternalSubnet: %v", err)
	}
	if got.CIDR != "192.0.2.0/24" {
		t.Errorf("CIDR = %q, want canonicalized 192.0.2.0/24", got.CIDR)
	}
	if got.Source != "manual" {
		t.Errorf("empty source defaulted to %q, want manual", got.Source)
	}
}
