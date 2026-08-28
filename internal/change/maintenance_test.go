// SPDX-License-Identifier: Apache-2.0

package change

// maintenance_test.go is T-4007's acceptance-criteria coverage at the
// change.Service layer: declaring a window (zone-required, invalid-range
// refusals), the boundary-instant Active() contract, the calendar render,
// and the MaintenanceState read model. internal/findings/maintenance_test.go
// covers the actual suppression decoration (Nodes-scoped, visible-not-
// omitted, and the notification-suppression side).

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

func newMaintenanceTestService(t *testing.T, now *int64) *Service {
	t.Helper()
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:         store.NewChangesetRepo(db),
		Audit:              store.NewAuditRepo(db),
		MaintenanceWindows: store.NewMaintenanceWindowRepo(db),
		ProtectedPath:      t.TempDir() + "/protected.json",
		Now:                func() time.Time { return time.Unix(*now, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// --- declare-time validation -----------------------------------------------

func TestDeclareMaintenanceWindow_RequiresNode(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	_, err := svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
		Zone: "UTC", StartLocal: "2026-09-01T02:00:00", EndLocal: "2026-09-01T06:00:00",
	})
	var invalid *ErrMaintenanceWindowInvalid
	if err == nil {
		t.Fatal("DeclareMaintenanceWindow with no node succeeded, want *ErrMaintenanceWindowInvalid")
	}
	if !asMaintenanceInvalid(err, &invalid) || invalid.Field != "node" {
		t.Fatalf("err = %v, want field=node", err)
	}
}

func TestDeclareMaintenanceWindow_RequiresExplicitZone(t *testing.T) {
	// T-4006's line held: a wall-clock-declared window with no zone is
	// refused at load, exactly like a freeze rule naming a local-wall-clock
	// fact with no Zone.
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	_, err := svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
		Node: "pvecube", StartLocal: "2026-09-01T02:00:00", EndLocal: "2026-09-01T06:00:00",
	})
	var invalid *ErrMaintenanceWindowInvalid
	if err == nil {
		t.Fatal("DeclareMaintenanceWindow with no zone succeeded, want *ErrMaintenanceWindowInvalid")
	}
	if !asMaintenanceInvalid(err, &invalid) || invalid.Field != "zone" {
		t.Fatalf("err = %v, want field=zone", err)
	}
}

func TestDeclareMaintenanceWindow_RejectsUnknownZone(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	_, err := svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "Not/ARealZone", StartLocal: "2026-09-01T02:00:00", EndLocal: "2026-09-01T06:00:00",
	})
	if err == nil {
		t.Fatal("DeclareMaintenanceWindow with an unloadable zone succeeded, want an error")
	}
}

func TestDeclareMaintenanceWindow_RejectsEndNotAfterStart(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	for _, end := range []string{"2026-09-01T02:00:00", "2026-09-01T01:00:00"} {
		_, err := svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
			Node: "pvecube", Zone: "UTC", StartLocal: "2026-09-01T02:00:00", EndLocal: end,
		})
		if err == nil {
			t.Fatalf("DeclareMaintenanceWindow(end=%s) succeeded, want a rejected zero-length/backwards window", end)
		}
	}
}

func TestDeclareMaintenanceWindow_RequiresConfiguredStore(t *testing.T) {
	now := int64(1_700_000_000)
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		// MaintenanceWindows deliberately left nil.
		Now: func() time.Time { return time.Unix(now, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2026-09-01T02:00:00", EndLocal: "2026-09-01T06:00:00",
	})
	var notConfigured *ErrMaintenanceWindowNotConfigured
	if err == nil || !asMaintenanceNotConfigured(err, &notConfigured) {
		t.Fatalf("err = %v, want *ErrMaintenanceWindowNotConfigured", err)
	}
}

func TestDeclareMaintenanceWindow_ResolvesLocalWallClockToAbsoluteInstant(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	// 2026-09-05T02:00:00 America/New_York (EDT, UTC-4) is 06:00 UTC.
	w, err := svc.DeclareMaintenanceWindow(context.Background(), "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "America/New_York", Reason: "firmware", StartLocal: "2026-09-05T02:00:00", EndLocal: "2026-09-05T06:00:00",
	})
	if err != nil {
		t.Fatalf("DeclareMaintenanceWindow: %v", err)
	}
	wantStart := time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC).Unix()
	wantEnd := time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC).Unix()
	if w.Start != wantStart || w.End != wantEnd {
		t.Fatalf("Start/End = %d/%d, want %d/%d (DST-correct UTC instants)", w.Start, w.End, wantStart, wantEnd)
	}
	if w.ID == "" || w.CreatedBy != "alice" || w.Node != "pvecube" || w.Reason != "firmware" {
		t.Fatalf("w = %+v, unexpected", w)
	}
}

// --- Active() boundary contract ---------------------------------------------

func TestMaintenanceWindow_ActiveBoundaries(t *testing.T) {
	w := MaintenanceWindow{Start: 1000, End: 2000}
	cases := []struct {
		name string
		at   int64
		want bool
	}{
		{"before start", 999, false},
		{"at start (inclusive)", 1000, true},
		{"mid-window", 1500, true},
		{"one second before end", 1999, true},
		{"at end (exclusive — AC3's boundary)", 2000, false},
		{"after end", 2001, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := w.Active(time.Unix(c.at, 0))
			if got != c.want {
				t.Errorf("Active(%d) = %v, want %v", c.at, got, c.want)
			}
		})
	}
}

// --- list / delete / calendar / read model ----------------------------------

func TestMaintenanceWindows_ListAndDelete(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	ctx := context.Background()
	w, declErr := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2026-09-01T02:00:00", EndLocal: "2026-09-01T06:00:00",
	})
	if declErr != nil {
		t.Fatalf("DeclareMaintenanceWindow: %v", declErr)
	}
	list, listErr := svc.MaintenanceWindows(ctx)
	if listErr != nil {
		t.Fatalf("MaintenanceWindows: %v", listErr)
	}
	if len(list) != 1 || list[0].ID != w.ID {
		t.Fatalf("MaintenanceWindows = %+v, want [%+v]", list, w)
	}
	if err := svc.DeleteMaintenanceWindow(ctx, "bob", w.ID); err != nil {
		t.Fatalf("DeleteMaintenanceWindow: %v", err)
	}
	list, listErr = svc.MaintenanceWindows(ctx)
	if listErr != nil {
		t.Fatalf("MaintenanceWindows after delete: %v", listErr)
	}
	if len(list) != 0 {
		t.Fatalf("MaintenanceWindows after delete = %+v, want empty", list)
	}
	// Deleting again (already gone) must not be an error.
	if err := svc.DeleteMaintenanceWindow(ctx, "bob", w.ID); err != nil {
		t.Fatalf("DeleteMaintenanceWindow(absent): %v, want nil", err)
	}
}

func TestCalendar_IncludesMaintenanceWindows(t *testing.T) {
	now := int64(1_700_000_000) // 2023-11-14T22:13:20Z
	svc := newMaintenanceTestService(t, &now)
	ctx := context.Background()

	// Active at `now`.
	if _, err := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2023-11-14T20:00:00", EndLocal: "2023-11-15T00:00:00",
	}); err != nil {
		t.Fatalf("DeclareMaintenanceWindow(active): %v", err)
	}
	// Not active at `now` (in the past).
	if _, err := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pve001", Zone: "UTC", StartLocal: "2023-01-01T00:00:00", EndLocal: "2023-01-01T04:00:00",
	}); err != nil {
		t.Fatalf("DeclareMaintenanceWindow(past): %v", err)
	}

	view, err := svc.Calendar(ctx)
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	if len(view.MaintenanceWindows) != 2 {
		t.Fatalf("MaintenanceWindows = %+v, want 2", view.MaintenanceWindows)
	}
	var sawActive, sawInactive bool
	for _, w := range view.MaintenanceWindows {
		switch w.Node {
		case "pvecube":
			sawActive = w.Active
		case "pve001":
			sawInactive = !w.Active
		}
	}
	if !sawActive {
		t.Error("pvecube's window should render Active: true")
	}
	if !sawInactive {
		t.Error("pve001's window should render Active: false")
	}
}

func TestMaintenanceState_ReadModel(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	ctx := context.Background()

	// No window at all: inactive, no dangling window pointer.
	state, stateErr := svc.MaintenanceState(ctx, "pvecube")
	if stateErr != nil {
		t.Fatalf("MaintenanceState: %v", stateErr)
	}
	if state.Active || state.Window != nil {
		t.Fatalf("MaintenanceState (no windows) = %+v, want inactive/nil", state)
	}

	if _, err := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2023-11-14T20:00:00", EndLocal: "2023-11-15T00:00:00",
	}); err != nil {
		t.Fatalf("DeclareMaintenanceWindow: %v", err)
	}
	state, stateErr = svc.MaintenanceState(ctx, "pvecube")
	if stateErr != nil {
		t.Fatalf("MaintenanceState: %v", stateErr)
	}
	if !state.Active || state.Window == nil || state.Window.Node != "pvecube" {
		t.Fatalf("MaintenanceState (active window) = %+v, want active with the window attached", state)
	}

	// A different node is unaffected — the scoping the whole card depends on.
	other, err := svc.MaintenanceState(ctx, "pve001")
	if err != nil {
		t.Fatalf("MaintenanceState(pve001): %v", err)
	}
	if other.Active {
		t.Fatalf("MaintenanceState(pve001) = %+v, want inactive (window is scoped to pvecube only)", other)
	}
}

func TestMaintenanceState_PicksSoonestEndingOverlap(t *testing.T) {
	now := int64(1_700_000_000)
	svc := newMaintenanceTestService(t, &now)
	ctx := context.Background()

	base := time.Unix(now, 0).UTC()
	_ = base
	if _, err := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2023-11-14T00:00:00", EndLocal: "2023-11-20T00:00:00",
	}); err != nil {
		t.Fatalf("DeclareMaintenanceWindow(long): %v", err)
	}
	soon, err := svc.DeclareMaintenanceWindow(ctx, "alice", MaintenanceWindowInput{
		Node: "pvecube", Zone: "UTC", StartLocal: "2023-11-14T00:00:00", EndLocal: "2023-11-15T00:00:00",
	})
	if err != nil {
		t.Fatalf("DeclareMaintenanceWindow(soon): %v", err)
	}
	state, err := svc.MaintenanceState(ctx, "pvecube")
	if err != nil {
		t.Fatalf("MaintenanceState: %v", err)
	}
	if state.Window == nil || state.Window.ID != soon.ID {
		t.Fatalf("MaintenanceState.Window = %+v, want the soonest-ending window %+v", state.Window, soon)
	}
}

func asMaintenanceInvalid(err error, target **ErrMaintenanceWindowInvalid) bool {
	e, ok := err.(*ErrMaintenanceWindowInvalid)
	if ok {
		*target = e
	}
	return ok
}

func asMaintenanceNotConfigured(err error, target **ErrMaintenanceWindowNotConfigured) bool {
	e, ok := err.(*ErrMaintenanceWindowNotConfigured)
	if ok {
		*target = e
	}
	return ok
}
