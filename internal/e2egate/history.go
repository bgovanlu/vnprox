// SPDX-License-Identifier: Apache-2.0

package e2egate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HistoryFile is the name of the append-only run log inside the history
// directory.
//
// JSON Lines, not one file per run: the trend has to read the last N runs on
// every gate invocation, and a directory scan that grows without bound is the
// kind of thing nobody notices until it is 40,000 files.
const HistoryFile = "runs.jsonl"

// MaxHistoryRuns is how many runs the log keeps. Older lines are dropped on
// append. Enough to see a 1-in-20 flake twice; small enough that the file stays
// a few hundred kilobytes.
const MaxHistoryRuns = 50

// TrendWindow is the default number of most-recent runs a flake rate is
// computed over.
const TrendWindow = 20

// RunRecord is one full-suite run, as recorded for the trend.
type RunRecord struct {
	StartedAt time.Time `json:"started_at"`
	RunID     string    `json:"run_id"`
	Commit    string    `json:"commit"`
	Host      string    `json:"host"`
	// Tests sits between the strings and the bool for field alignment: a slice
	// carries its pointer in its first word and sixteen pointer-free bytes
	// after it, so it is the cheapest field to end the pointer region with.
	Tests []RecordedTest `json:"tests"`
	// Complete distinguishes a full-suite run from a targeted one. Only
	// complete runs count towards a flake rate: a test that did not run in a
	// grep-filtered run has not passed, and counting it as such would dilute
	// every rate towards zero.
	Complete bool `json:"complete"`
}

// RecordedTest is one test's outcome inside a RunRecord.
type RecordedTest struct {
	File       string `json:"file"`
	Title      string `json:"title"`
	Status     Status `json:"status"`
	Shard      string `json:"shard"`
	DurationMS int64  `json:"duration_ms"`
}

// Key matches Outcome.Key.
func (r RecordedTest) Key() string { return r.File + TitleSeparator + r.Title }

// NewRunRecord flattens shard reports into one record.
func NewRunRecord(runID, commit, host string, startedAt time.Time, complete bool, reports []ShardReport) RunRecord {
	rec := RunRecord{
		RunID:     runID,
		StartedAt: startedAt,
		Commit:    commit,
		Host:      host,
		Complete:  complete,
	}
	for _, rep := range reports {
		for _, o := range rep.Outcomes {
			rec.Tests = append(rec.Tests, RecordedTest{
				File:       o.File,
				Title:      o.Title,
				Status:     o.Status,
				DurationMS: o.DurationMS,
				Shard:      o.Shard,
			})
		}
	}
	sort.Slice(rec.Tests, func(i, j int) bool { return rec.Tests[i].Key() < rec.Tests[j].Key() })
	return rec
}

// AppendRun writes rec to the history log, trimming to MaxHistoryRuns.
func AppendRun(dir string, rec RunRecord) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating e2e history dir %s: %w", dir, err)
	}
	runs, err := LoadRuns(dir, 0)
	if err != nil {
		return err
	}
	runs = append(runs, rec)
	if len(runs) > MaxHistoryRuns {
		runs = runs[len(runs)-MaxHistoryRuns:]
	}

	path := filepath.Join(dir, HistoryFile)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", tmp, err)
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range runs {
		if err := enc.Encode(r); err != nil {
			_ = f.Close()
			return fmt.Errorf("encoding run %s: %w", r.RunID, err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return fmt.Errorf("flushing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// LoadRuns reads the history log. last <= 0 means every run kept. A missing log
// is an empty history, not an error — the first run has no history to read.
func LoadRuns(dir string, last int) ([]RunRecord, error) {
	path := filepath.Join(dir, HistoryFile)
	f, err := os.Open(path) //nolint:gosec // path is derived from a caller-named tooling directory.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var runs []RunRecord
	sc := bufio.NewScanner(f)
	// A run record holds one line per suite; 89 tests of JSON comfortably
	// exceeds bufio's 64 KiB default.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec RunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parsing a run record in %s: %w", path, err)
		}
		runs = append(runs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if last > 0 && len(runs) > last {
		runs = runs[len(runs)-last:]
	}
	return runs, nil
}
