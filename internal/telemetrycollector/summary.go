// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

// summary.go is T-3710's deliverable 4: "a way to read what arrives ...
// without a human hand-writing SQL each time." Summary is deliberately an
// aggregate, never a row dump — GET /v1/summary and `vnproxtelemetryd
// report` both answer "which PVE versions are vnprox installations
// actually running against", not "list every submission", which is the
// line between a report and a dashboard the task card draws.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// CheckOutcome is one check id's pass/fail/skip tally across every stored
// submission.
type CheckOutcome struct {
	CheckID string `json:"checkId"`
	Pass    int64  `json:"pass"`
	Fail    int64  `json:"fail"`
	Skip    int64  `json:"skip"`
}

// Summary is the aggregate view over every stored submission.
type Summary struct {
	// GeneratedAt is when this summary was computed (collector's clock,
	// same as ReceivedAt — never a client-supplied time).
	GeneratedAt time.Time `json:"generatedAt"`
	// OldestReceivedAt and NewestReceivedAt bound the stored window. Zero
	// when there are no submissions.
	OldestReceivedAt time.Time `json:"oldestReceivedAt,omitempty"`
	NewestReceivedAt time.Time `json:"newestReceivedAt,omitempty"`
	// PVEVersions counts submissions per pveVersion string — the headline
	// answer to "which PVE versions are vnprox installations actually
	// running against".
	PVEVersions map[string]int64 `json:"pveVersions"`
	// VnproxVersions counts submissions per vnproxVersion string.
	VnproxVersions map[string]int64 `json:"vnproxVersions"`
	// Suites counts submissions per suite.
	Suites map[string]int64 `json:"suites"`
	// Checks is per-check-id pass/fail/skip tallies, sorted by CheckID.
	Checks []CheckOutcome `json:"checks"`
	// TotalSubmissions is the row count.
	TotalSubmissions int64 `json:"totalSubmissions"`
	// DistinctInstalls is the number of distinct install-ids represented.
	DistinctInstalls int64 `json:"distinctInstalls"`
}

// checkRow mirrors telemetry.CheckVerdict's wire shape for decoding the
// stored checks JSON column — kept local rather than importing the type so
// this package's decode path does not silently start accepting a shape
// telemetry.CheckVerdict has moved away from.
type checkRow struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
}

// BuildSummary computes Summary over every row currently stored.
func (s *Store) BuildSummary(ctx context.Context, now time.Time) (Summary, error) {
	sum := Summary{
		GeneratedAt:    now,
		PVEVersions:    map[string]int64{},
		VnproxVersions: map[string]int64{},
		Suites:         map[string]int64{},
	}

	total, err := s.Count(ctx)
	if err != nil {
		return Summary{}, err
	}
	sum.TotalSubmissions = total
	if total == 0 {
		return sum, nil
	}

	if err = s.sqlDB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT install_id) FROM submissions`).Scan(&sum.DistinctInstalls); err != nil {
		return Summary{}, fmt.Errorf("telemetrycollector: counting distinct install ids: %w", err)
	}

	var oldest, newest int64
	if err = s.sqlDB.QueryRowContext(ctx, `SELECT MIN(received_at), MAX(received_at) FROM submissions`).Scan(&oldest, &newest); err != nil {
		return Summary{}, fmt.Errorf("telemetrycollector: reading submission time bounds: %w", err)
	}
	sum.OldestReceivedAt = time.Unix(oldest, 0).UTC()
	sum.NewestReceivedAt = time.Unix(newest, 0).UTC()

	if err = s.countInto(ctx, `SELECT pve_version, COUNT(*) FROM submissions GROUP BY pve_version`, sum.PVEVersions); err != nil {
		return Summary{}, err
	}
	if err = s.countInto(ctx, `SELECT vnprox_version, COUNT(*) FROM submissions GROUP BY vnprox_version`, sum.VnproxVersions); err != nil {
		return Summary{}, err
	}
	if err = s.countInto(ctx, `SELECT suite, COUNT(*) FROM submissions GROUP BY suite`, sum.Suites); err != nil {
		return Summary{}, err
	}

	checks, err := s.checkOutcomes(ctx)
	if err != nil {
		return Summary{}, err
	}
	sum.Checks = checks

	return sum, nil
}

func (s *Store) countInto(ctx context.Context, query string, into map[string]int64) error {
	rows, err := s.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("telemetrycollector: running %q: %w", query, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var n int64
		if scanErr := rows.Scan(&key, &n); scanErr != nil {
			return fmt.Errorf("telemetrycollector: scanning aggregate row: %w", scanErr)
		}
		into[key] = n
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("telemetrycollector: iterating aggregate rows: %w", err)
	}
	return nil
}

// checkOutcomes tallies pass/fail/skip per check id across every stored
// submission's checks column. Decoded in Go rather than with SQLite JSON
// functions — the driver (modernc.org/sqlite, T-002's pure-Go choice) does
// not ship SQLite's optional JSON1 extension, and this table is small
// enough (one row per verify run an operator chose to send) that a
// row-at-a-time decode is not a real cost.
func (s *Store) checkOutcomes(ctx context.Context) ([]CheckOutcome, error) {
	rows, err := s.sqlDB.QueryContext(ctx, `SELECT checks FROM submissions`)
	if err != nil {
		return nil, fmt.Errorf("telemetrycollector: reading checks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tallies := map[string]*CheckOutcome{}
	for rows.Next() {
		var raw string
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return nil, fmt.Errorf("telemetrycollector: scanning checks column: %w", scanErr)
		}
		var checks []checkRow
		if jsonErr := json.Unmarshal([]byte(raw), &checks); jsonErr != nil {
			// A row that was accepted by Guard at submit time cannot fail
			// to decode here; fail loudly rather than silently skip a row
			// if that invariant is ever broken.
			return nil, fmt.Errorf("telemetrycollector: decoding stored checks: %w", jsonErr)
		}
		for _, c := range checks {
			t, ok := tallies[c.ID]
			if !ok {
				t = &CheckOutcome{CheckID: c.ID}
				tallies[c.ID] = t
			}
			switch c.Status {
			case "pass":
				t.Pass++
			case "fail":
				t.Fail++
			case "skip":
				t.Skip++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetrycollector: iterating checks rows: %w", err)
	}

	out := make([]CheckOutcome, 0, len(tallies))
	for _, t := range tallies {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CheckID < out[j].CheckID })
	return out, nil
}
