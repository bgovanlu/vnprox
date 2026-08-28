// SPDX-License-Identifier: Apache-2.0

package soak

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	_ "modernc.org/sqlite" // the store's own driver; this package only reads row counts
)

func TestGoroutineSamplerTracksRealGoroutines(t *testing.T) {
	s := Goroutines()
	if s.Name() != MetricGoroutines || s.Unit() != "goroutines" {
		t.Fatalf("Goroutines() named %q/%q", s.Name(), s.Unit())
	}
	before, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	const spawned = 25
	release := make(chan struct{})
	started := make(chan struct{}, spawned)
	for range spawned {
		go func() {
			started <- struct{}{}
			<-release
		}()
	}
	for range spawned {
		<-started
	}
	during, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	close(release)

	if during < before+spawned {
		t.Errorf("goroutine sampler read %v after spawning %d goroutines from a base of %v", during, spawned, before)
	}
}

func TestHeapSamplerSeesRetainedMemory(t *testing.T) {
	s := Heap(true)
	if s.Name() != MetricHeapBytes {
		t.Fatalf("Heap() named %q", s.Name())
	}
	before, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	// 8 MiB held live across the second sample: forcing a GC first is what
	// makes this a measurement of retention rather than of allocator noise.
	held := make([]byte, 8<<20)
	for i := range held {
		held[i] = byte(i)
	}
	during, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	runtime.KeepAlive(held)
	if during <= before {
		t.Errorf("heap sampler read %v after retaining 8 MiB from a base of %v", during, before)
	}
}

func TestProcSamplers(t *testing.T) {
	if !ProcSamplersAvailable() {
		t.Skip("/proc is not available on this host; RSS and fd sampling are Linux-only by design")
	}
	rss := RSS(0)
	if rss.Name() != MetricRSSBytes {
		t.Fatalf("RSS() named %q", rss.Name())
	}
	v, err := rss.Sample(context.Background())
	if err != nil {
		t.Fatalf("RSS Sample: %v", err)
	}
	if v <= 0 {
		t.Errorf("RSS sampled %v, want a positive byte count", v)
	}

	fds := OpenFDs(0)
	if fds.Name() != MetricOpenFDs {
		t.Fatalf("OpenFDs() named %q", fds.Name())
	}
	before, err := fds.Sample(context.Background())
	if err != nil {
		t.Fatalf("OpenFDs Sample: %v", err)
	}
	f, err := openTempFiles(t, 5)
	if err != nil {
		t.Fatalf("opening temp files: %v", err)
	}
	during, err := fds.Sample(context.Background())
	if err != nil {
		t.Fatalf("OpenFDs Sample: %v", err)
	}
	closeAll(f)
	if during < before+5 {
		t.Errorf("fd sampler read %v after opening 5 files from a base of %v", during, before)
	}
}

func openTempFiles(t *testing.T, n int) ([]*os.File, error) {
	t.Helper()
	dir := t.TempDir()
	files := make([]*os.File, 0, n)
	for i := range n {
		f, err := os.Create(filepath.Join(dir, "fd"+strconv.Itoa(i)))
		if err != nil {
			closeAll(files)
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func TestRSSSamplerReportsAMissingProcEntry(t *testing.T) {
	// pid 0 means self; a pid that cannot exist exercises the error path
	// rather than silently reporting zero, which would read as "flat".
	if _, err := RSS(1 << 30).Sample(context.Background()); err == nil {
		t.Fatal("RSS sampled a nonexistent pid without error")
	}
	if _, err := OpenFDs(1 << 30).Sample(context.Background()); err == nil {
		t.Fatal("OpenFDs sampled a nonexistent pid without error")
	}
}

func TestTableSamplers(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "soak.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE audit_log (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE metric_samples (id INTEGER PRIMARY KEY)`,
		`INSERT INTO audit_log (id) VALUES (1), (2), (3)`,
	} {
		if _, execErr := db.ExecContext(ctx, stmt); execErr != nil {
			t.Fatalf("exec %q: %v", stmt, execErr)
		}
	}

	samplers, err := TableSamplers(ctx, db)
	if err != nil {
		t.Fatalf("TableSamplers: %v", err)
	}
	byName := make(map[string]Sampler, len(samplers))
	for _, s := range samplers {
		byName[s.Name()] = s
	}
	// Every user table is enumerated automatically — that is the whole
	// point: the table a future task forgets to prune is watched without
	// anyone remembering to add it here.
	for _, want := range []string{TablePrefix + "audit_log", TablePrefix + "metric_samples"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("TableSamplers produced %v, missing %q", byName, want)
		}
	}

	got, err := byName[TablePrefix+"audit_log"].Sample(ctx)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if got != 3 {
		t.Errorf("audit_log row count = %v, want 3", got)
	}
	if u := byName[TablePrefix+"audit_log"].Unit(); u != "rows" {
		t.Errorf("table sampler unit = %q, want %q", u, "rows")
	}

	if _, insertErr := db.ExecContext(ctx, `INSERT INTO audit_log (id) VALUES (4), (5)`); insertErr != nil {
		t.Fatalf("insert: %v", insertErr)
	}
	got, err = byName[TablePrefix+"audit_log"].Sample(ctx)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if got != 5 {
		t.Errorf("audit_log row count after inserts = %v, want 5", got)
	}
}

func TestSafeTableName(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"audit_log":           true,
		"metric_samples":      true,
		"t1":                  true,
		"_leading_underscore": true,
		"":                    false,
		"1leading_digit":      false,
		`bad"quote`:           false,
		"has space":           false,
		"drop;table":          false,
	}
	for name, want := range tests {
		if got := safeTableName(name); got != want {
			t.Errorf("safeTableName(%q) = %v, want %v", name, got, want)
		}
	}
}
