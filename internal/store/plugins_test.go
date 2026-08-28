// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPluginRepo_UpsertGetListRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewPluginRepo(openTestDB(t))

	row := PluginRow{
		ID:              "com.acme.driver",
		Name:            "Acme SONiC Driver",
		Version:         "2.1.0",
		APIVersion:      "v1",
		Transport:       "grpc",
		Endpoint:        "/usr/lib/vnprox/acme-driver",
		InstalledBy:     "root@pam",
		ExtensionPoints: []string{"switchDriver"},
		Capabilities:    []string{"netRead", "netWrite"},
		InstalledAt:     1700000000,
		Enabled:         true,
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, row) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, row)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != row.ID {
		t.Errorf("List = %+v, want one row for %q", list, row.ID)
	}
}

func TestPluginRepo_SetEnabledAndDelete(t *testing.T) {
	ctx := context.Background()
	repo := NewPluginRepo(openTestDB(t))
	row := PluginRow{
		ID: "com.acme.tile", Name: "Tile", APIVersion: "v1", Transport: "in-process",
		ExtensionPoints: []string{"dashboardTile"}, Capabilities: []string{"netRead"},
		InstalledBy: "root@pam", InstalledAt: 1700000000, Enabled: true,
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.SetEnabled(ctx, row.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, _ := repo.Get(ctx, row.ID)
	if got.Enabled {
		t.Error("SetEnabled(false) did not persist")
	}

	if err := repo.SetEnabled(ctx, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetEnabled(unknown) = %v, want ErrNotFound", err)
	}

	if err := repo.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(twice) = %v, want ErrNotFound", err)
	}
}

func TestPluginRepo_EmptyListSlicesRoundTripAsEmpty(t *testing.T) {
	ctx := context.Background()
	repo := NewPluginRepo(openTestDB(t))
	// A plugin with no capabilities/extension points must round-trip as empty
	// slices, never nil, so the JSON columns stay "[]".
	row := PluginRow{
		ID: "com.acme.empty", Name: "Empty", APIVersion: "v1", Transport: "in-process",
		InstalledBy: "root@pam", InstalledAt: 1,
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ExtensionPoints == nil || got.Capabilities == nil {
		t.Errorf("empty slices decoded as nil: %+v", got)
	}
	if len(got.ExtensionPoints) != 0 || len(got.Capabilities) != 0 {
		t.Errorf("expected empty slices, got %+v", got)
	}
}
