// SPDX-License-Identifier: Apache-2.0

package soak

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Artifact file names inside the directory WriteArtifact is given.
const (
	SamplesFile = "samples.csv"
	ReportFile  = "report.json"
)

// ArtifactReport is the JSON side of the artifact: everything needed to
// understand — and re-run — a failure without the daemon still being up.
//
// Seed is the point of the whole struct (T-2504 AC4): the churn generator
// is seeded, and the seed travels with the evidence, so "re-run the exact
// sequence that failed" is a copy-paste rather than an archaeology project.
type ArtifactReport struct {
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	DurationSpec  string    `json:"duration"`
	Interval      string    `json:"sample_interval"`
	ChurnInterval string    `json:"churn_interval"`
	Rerun         string    `json:"rerun"`
	Report        Report    `json:"report"`
	Seed          uint64    `json:"seed"`
	SampleCount   int       `json:"sample_count"`
	ChurnTicks    int       `json:"churn_ticks"`
	ChurnErrors   int       `json:"churn_errors"`
}

// WriteArtifact writes the sample series (CSV) and the verdict (JSON) into
// dir, creating it if needed, and returns the paths written.
//
// Called for a failing run *and* a passing one. A gate that only leaves
// evidence when it fails cannot answer "was it already creeping last
// week?", which is the question a trend gate exists to make answerable.
func WriteArtifact(dir string, res *Result) ([]string, error) {
	if res == nil {
		return nil, fmt.Errorf("soak: no result to write")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating soak artifact directory %s: %w", dir, err)
	}

	csvPath := filepath.Join(dir, SamplesFile)
	if err := writeSamplesCSV(csvPath, res); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(dir, ReportFile)
	if err := writeReportJSON(jsonPath, res); err != nil {
		return []string{csvPath}, err
	}
	return []string{csvPath, jsonPath}, nil
}

func writeSamplesCSV(path string, res *Result) (err error) {
	f, err := os.Create(path) //nolint:gosec // artifact path chosen by the caller (a make target / test tempdir)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing %s: %w", path, cerr)
		}
	}()

	series := append([]Series(nil), res.Series...)
	sort.Slice(series, func(i, j int) bool { return series[i].Metric < series[j].Metric })

	w := csv.NewWriter(f)
	header := make([]string, 0, len(series)+1)
	header = append(header, "elapsed_s")
	rows := 0
	for _, s := range series {
		header = append(header, s.Metric)
		rows = max(rows, len(s.Values))
	}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("writing %s header: %w", path, err)
	}
	for i := range rows {
		rec := make([]string, 0, len(series)+1)
		elapsed := ""
		for _, s := range series {
			if i < len(s.Elapsed) {
				elapsed = strconv.FormatFloat(s.Elapsed[i]*60, 'f', 1, 64)
				break
			}
		}
		rec = append(rec, elapsed)
		for _, s := range series {
			if i < len(s.Values) {
				rec = append(rec, strconv.FormatFloat(s.Values[i], 'f', -1, 64))
			} else {
				rec = append(rec, "")
			}
		}
		if err := w.Write(rec); err != nil {
			return fmt.Errorf("writing %s row %d: %w", path, i, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flushing %s: %w", path, err)
	}
	return nil
}

func writeReportJSON(path string, res *Result) error {
	ar := ArtifactReport{
		Seed:          res.Seed,
		StartedAt:     res.StartedAt,
		EndedAt:       res.EndedAt,
		DurationSpec:  res.Duration.String(),
		Interval:      res.Interval.String(),
		ChurnInterval: res.ChurnInterval.String(),
		SampleCount:   res.SampleCount,
		ChurnTicks:    res.ChurnTicks,
		ChurnErrors:   res.ChurnErrors,
		Report:        res.Report,
		Rerun: fmt.Sprintf("make soak SOAK_DURATION=%s SOAK_INTERVAL=%s SOAK_SEED=%d",
			res.Duration, res.Interval, res.Seed),
	}
	data, err := json.MarshalIndent(ar, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
