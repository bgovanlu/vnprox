// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/telemetry"
)

func statPerm(path string) (fs.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "telemetry.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func samplePayload(installID string) telemetry.Payload {
	return telemetry.Payload{
		PayloadVersion: telemetry.PayloadVersion,
		InstallID:      installID,
		VnproxVersion:  "3.0.3",
		PVEVersion:     "pve-manager/9.2.4",
		Kernel:         "6.8.12-4-pve",
		NICPCIIDs:      []string{"0x8086:0x1521"},
		NodeCount:      2,
		Suite:          "hardware",
		Checks: []telemetry.CheckVerdict{
			{ID: "drift.config_vs_live", Status: "pass", DurationMS: 412},
			{ID: "iface.lacp_partner_observed", Status: "fail", DurationMS: 1203},
		},
	}
}

func TestStoreInsertAndCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if n, err := store.Count(ctx); err != nil || n != 0 {
		t.Fatalf("Count on empty store: %d, %v", n, err)
	}

	if err := store.Insert(ctx, samplePayload("01HZY0Z1QW8V9N7M3K5R2T4B6D"), time.Unix(1000, 0)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if n, err := store.Count(ctx); err != nil || n != 1 {
		t.Fatalf("Count after insert: %d, %v", n, err)
	}
}

func TestStoreDeleteByInstallID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	const idA = "01HZY0Z1QW8V9N7M3K5R2T4B6D"
	const idB = "01HZY0Z1QW8V9N7M3K5R2T4B6E"
	for i := 0; i < 3; i++ {
		if err := store.Insert(ctx, samplePayload(idA), time.Unix(int64(1000+i), 0)); err != nil {
			t.Fatalf("Insert idA: %v", err)
		}
	}
	if err := store.Insert(ctx, samplePayload(idB), time.Unix(2000, 0)); err != nil {
		t.Fatalf("Insert idB: %v", err)
	}

	n, err := store.DeleteByInstallID(ctx, idA)
	if err != nil {
		t.Fatalf("DeleteByInstallID: %v", err)
	}
	if n != 3 {
		t.Fatalf("deleted = %d, want 3", n)
	}

	total, err := store.Count(ctx)
	if err != nil || total != 1 {
		t.Fatalf("Count after delete: %d, %v", total, err)
	}

	// Idempotent: deleting an id with nothing left returns 0, not an
	// error — the response must not let a caller distinguish "never
	// existed" from "already deleted".
	n, err = store.DeleteByInstallID(ctx, idA)
	if err != nil || n != 0 {
		t.Fatalf("second DeleteByInstallID: %d, %v", n, err)
	}
	n, err = store.DeleteByInstallID(ctx, "01HZY0Z1QW8V9N7M3K5R2T4B6F")
	if err != nil || n != 0 {
		t.Fatalf("DeleteByInstallID on an id never seen: %d, %v", n, err)
	}
}

func TestStorePrune(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Insert(ctx, samplePayload("01HZY0Z1QW8V9N7M3K5R2T4B6D"), old); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := store.Insert(ctx, samplePayload("01HZY0Z1QW8V9N7M3K5R2T4B6E"), recent); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	n, err := store.Prune(ctx, cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	total, err := store.Count(ctx)
	if err != nil || total != 1 {
		t.Fatalf("Count after prune: %d, %v", total, err)
	}
}

func TestBuildSummary(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if sum, err := store.BuildSummary(ctx, time.Unix(5000, 0)); err != nil {
		t.Fatalf("BuildSummary on empty store: %v", err)
	} else if sum.TotalSubmissions != 0 {
		t.Fatalf("TotalSubmissions = %d, want 0", sum.TotalSubmissions)
	}

	p1 := samplePayload("01HZY0Z1QW8V9N7M3K5R2T4B6D")
	p2 := samplePayload("01HZY0Z1QW8V9N7M3K5R2T4B6E")
	p2.PVEVersion = "pve-manager/8.3.5"
	p2.Checks = []telemetry.CheckVerdict{
		{ID: "drift.config_vs_live", Status: "pass", DurationMS: 90},
		{ID: "iface.lacp_partner_observed", Status: "skip", DurationMS: 1},
	}

	if err := store.Insert(ctx, p1, time.Unix(1000, 0)); err != nil {
		t.Fatalf("Insert p1: %v", err)
	}
	if err := store.Insert(ctx, p2, time.Unix(2000, 0)); err != nil {
		t.Fatalf("Insert p2: %v", err)
	}

	sum, err := store.BuildSummary(ctx, time.Unix(5000, 0))
	if err != nil {
		t.Fatalf("BuildSummary: %v", err)
	}
	if sum.TotalSubmissions != 2 {
		t.Fatalf("TotalSubmissions = %d, want 2", sum.TotalSubmissions)
	}
	if sum.DistinctInstalls != 2 {
		t.Fatalf("DistinctInstalls = %d, want 2", sum.DistinctInstalls)
	}
	wantPVE := map[string]int64{"pve-manager/9.2.4": 1, "pve-manager/8.3.5": 1}
	if !reflect.DeepEqual(sum.PVEVersions, wantPVE) {
		t.Fatalf("PVEVersions = %#v, want %#v", sum.PVEVersions, wantPVE)
	}
	if !sum.OldestReceivedAt.Equal(time.Unix(1000, 0).UTC()) {
		t.Fatalf("OldestReceivedAt = %v", sum.OldestReceivedAt)
	}
	if !sum.NewestReceivedAt.Equal(time.Unix(2000, 0).UTC()) {
		t.Fatalf("NewestReceivedAt = %v", sum.NewestReceivedAt)
	}

	var driftOutcome, lacpOutcome CheckOutcome
	for _, c := range sum.Checks {
		switch c.CheckID {
		case "drift.config_vs_live":
			driftOutcome = c
		case "iface.lacp_partner_observed":
			lacpOutcome = c
		}
	}
	if driftOutcome.Pass != 2 {
		t.Fatalf("drift.config_vs_live pass = %d, want 2", driftOutcome.Pass)
	}
	if lacpOutcome.Fail != 1 || lacpOutcome.Skip != 1 {
		t.Fatalf("iface.lacp_partner_observed fail/skip = %d/%d, want 1/1", lacpOutcome.Fail, lacpOutcome.Skip)
	}
}

// TestStoreColumnsMatchPayloadAndReceivedAt is this package's equivalent of
// internal/telemetry.TestDocSectionMatchesPayload: every column in
// submissions must trace back either to an internal/telemetry.Payload field
// (by name, snake_cased) or to one of the two collector-added columns this
// test names explicitly (id, the local primary key; received_at, the
// collector's own receipt clock — the one field payload.go documents as
// deliberately absent from the wire payload itself). A column added to the
// schema without being one of those two things fails this test, the same
// way an undocumented Payload field fails docs.go's check.
func TestStoreColumnsMatchPayloadAndReceivedAt(t *testing.T) {
	store := openTestStore(t)

	rows, err := store.sqlDB.QueryContext(context.Background(), `PRAGMA table_info(submissions)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dfltValue  any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &primaryKey); err != nil {
			t.Fatalf("scanning table_info row: %v", err)
		}
		columns = append(columns, name)
	}

	// collectorOwned is every column that is not a projection of a Payload
	// field, with the reason it is allowed to exist without one.
	collectorOwned := map[string]string{
		"id":          "local primary key, never sent or read back to any client",
		"received_at": "payload.go: 'a local clock is a fingerprint' — the client sends no timestamp; this is the collector's own receipt time",
	}

	wantFromPayload := map[string]bool{}
	for _, f := range telemetry.PayloadFields() {
		top, _, _ := strings.Cut(f.Name, "[]")
		wantFromPayload[camelToSnake(top)] = true
	}

	for _, col := range columns {
		if collectorOwned[col] != "" {
			continue
		}
		if !wantFromPayload[col] {
			t.Errorf("submissions.%s has no corresponding internal/telemetry.Payload field and is not in collectorOwned — either it is undocumented growth or collectorOwned needs a line explaining it", col)
		}
	}
	for field := range wantFromPayload {
		found := false
		for _, col := range columns {
			if col == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("internal/telemetry.Payload has a field that snake-cases to %q, but submissions has no such column", field)
		}
	}
}

// camelToSnake converts payload.go's JSON field names ("nicPciIds") to this
// package's column names ("nic_pci_ids").
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestCamelToSnake(t *testing.T) {
	cases := map[string]string{
		"payloadVersion": "payload_version",
		"installId":      "install_id",
		"nicPciIds":      "nic_pci_ids",
		"nodeCount":      "node_count",
		"suite":          "suite",
	}
	for in, want := range cases {
		if got := camelToSnake(in); got != want {
			t.Errorf("camelToSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenEnforcesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	info, err := statPerm(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info != dbFilePerm {
		t.Fatalf("db file mode = %o, want %o", info, dbFilePerm)
	}
}

func TestPathReturnsOpenedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if store.Path() != path {
		t.Fatalf("Path() = %q, want %q", store.Path(), path)
	}
}
