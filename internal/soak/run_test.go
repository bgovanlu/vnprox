package soak

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// leakingSampler grows by step on every read — the in-package stand-in for
// the real build-tagged leak fixtures, so Run/Analyze/WriteArtifact are
// proven end to end in milliseconds rather than only by a `make soak` run.
func leakingSampler(name, unit string, start, step float64) Sampler {
	var n atomic.Int64
	return SamplerFunc(name, unit, func(context.Context) (float64, error) {
		i := n.Add(1) - 1
		return start + step*float64(i), nil
	})
}

func flatSampler(name, unit string, at float64) Sampler {
	return SamplerFunc(name, unit, func(context.Context) (float64, error) { return at, nil })
}

func shortRunConfig() Config {
	return Config{
		Duration:      400 * time.Millisecond,
		Interval:      10 * time.Millisecond,
		ChurnInterval: 10 * time.Millisecond,
		Logger:        quietLogger(),
		Policy: Policy{
			Default:          0.5,
			MinWindowSamples: 4,
			PerMetric:        map[string]float64{TablePrefix: 1},
		},
	}
}

// TestRunFailsOnALeakingSampler is the end-to-end shape of AC1/AC2: a
// metric that climbs while the run is churning fails the gate, and the
// failure names the metric.
func TestRunFailsOnALeakingSampler(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	// ~1 unit per 10ms sample = 6000/min: unmistakably a leak at any
	// tolerance a real run would use.
	cfg.Samplers = []Sampler{
		leakingSampler(MetricGoroutines, "goroutines", 40, 1),
		flatSampler(MetricHeapBytes, "bytes", 12_000_000),
	}
	var churned atomic.Int64
	cfg.Churn = func(context.Context, int) error { churned.Add(1); return nil }

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Report.Pass {
		t.Fatal("gate passed against a sampler that climbed on every read")
	}
	gateErr := res.Report.Err()
	if gateErr == nil || !strings.Contains(gateErr.Error(), MetricGoroutines) {
		t.Fatalf("gate error %v does not name %q", gateErr, MetricGoroutines)
	}
	if strings.Contains(gateErr.Error(), MetricHeapBytes) {
		t.Errorf("gate error %v names the flat metric as a failure", gateErr)
	}
	if churned.Load() == 0 {
		t.Error("churn was never called")
	}
}

// TestRunPassesAFlatButHighSampler is AC3: allocate a lot once, hold it,
// and the gate must not care.
func TestRunPassesAFlatButHighSampler(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Samplers = []Sampler{
		flatSampler(MetricGoroutines, "goroutines", 5000),
		flatSampler(TablePrefix+"audit_log", "rows", 250_000),
	}
	cfg.Churn = func(context.Context, int) error { return nil }

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Report.Pass {
		t.Fatalf("gate failed a flat-but-high run: %v", res.Report.Err())
	}
}

// TestRunSamplesEveryMetricAtLeastTwice is AC5's floor, enforced in the
// units the gate actually runs in: at the shipped default interval, a
// 60-second run reads every sampler far more than twice.
func TestRunSamplesEveryMetricAtLeastTwice(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Duration = 60 * time.Millisecond
	cfg.Interval = 10 * time.Millisecond
	cfg.Policy.MinWindowSamples = 2
	cfg.Samplers = []Sampler{
		flatSampler(MetricGoroutines, "goroutines", 40),
		flatSampler(MetricOpenFDs, "fds", 12),
		flatSampler(TablePrefix+"changesets", "rows", 3),
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, s := range res.Series {
		if len(s.Values) < MinSamplesPerRun {
			t.Errorf("series %q has %d samples, want at least %d", s.Metric, len(s.Values), MinSamplesPerRun)
		}
		if len(s.Values) != len(s.Elapsed) {
			t.Errorf("series %q has %d values but %d timestamps", s.Metric, len(s.Values), len(s.Elapsed))
		}
		if len(s.Values) != res.SampleCount {
			t.Errorf("series %q has %d samples but the run recorded %d", s.Metric, len(s.Values), res.SampleCount)
		}
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	t.Parallel()
	base := shortRunConfig()
	base.Samplers = []Sampler{flatSampler(MetricGoroutines, "goroutines", 1)}

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no samplers", func(c *Config) { c.Samplers = nil }, "at least one sampler"},
		{"zero duration", func(c *Config) { c.Duration = 0 }, "duration must be positive"},
		{"zero interval", func(c *Config) { c.Interval = 0 }, "sample interval must be positive"},
		{"duplicate sampler names", func(c *Config) {
			c.Samplers = append(c.Samplers, flatSampler(MetricGoroutines, "goroutines", 2))
		}, "duplicate sampler name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			cfg.Samplers = append([]Sampler(nil), base.Samplers...)
			tc.mutate(&cfg)
			_, err := Run(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestRunFailsWhenChurnNeverWorks guards the quietest possible false pass:
// a soak that measured an idle daemon because every churn call errored.
func TestRunFailsWhenChurnNeverWorks(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Samplers = []Sampler{flatSampler(MetricGoroutines, "goroutines", 40)}
	cfg.Churn = func(context.Context, int) error { return errors.New("mock PVE is down") }

	res, err := Run(context.Background(), cfg)
	if !errors.Is(err, ErrChurnDead) {
		t.Fatalf("Run error = %v, want ErrChurnDead", err)
	}
	if res == nil {
		t.Fatal("Run returned no Result for a failed run; the artifact would be lost")
	}
	if res.ChurnErrors == 0 {
		t.Error("churn errors were not counted")
	}
}

// TestRunSurvivesOccasionalChurnFailures: a mock rejecting some operations
// is normal and must not fail the run on its own.
func TestRunSurvivesOccasionalChurnFailures(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Samplers = []Sampler{flatSampler(MetricGoroutines, "goroutines", 40)}
	cfg.Churn = func(_ context.Context, tick int) error {
		if tick%3 == 0 {
			return errors.New("transient")
		}
		return nil
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Report.Pass {
		t.Fatalf("gate failed a clean run: %v", res.Report.Err())
	}
	if res.ChurnErrors == 0 {
		t.Error("expected some churn errors to have been counted")
	}
}

func TestRunPropagatesSamplerFailure(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Samplers = []Sampler{
		SamplerFunc("broken", "rows", func(context.Context) (float64, error) {
			return 0, errors.New("table vanished")
		}),
	}
	res, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "sampling broken") {
		t.Fatalf("Run error = %v, want it to name the failing sampler", err)
	}
	if res == nil {
		t.Fatal("Run returned no Result alongside a sampler error")
	}
}

// TestRunWritesADiagnosableArtifact covers AC4 (the seed travels with the
// evidence) and the card's "emits the sample series as an artifact so a
// failure is diagnosable without a re-run".
func TestRunWritesADiagnosableArtifact(t *testing.T) {
	t.Parallel()
	cfg := shortRunConfig()
	cfg.Seed = 0xC0FFEE
	cfg.Samplers = []Sampler{
		leakingSampler(TablePrefix+"soak_leak_unbounded", "rows", 0, 5),
		flatSampler(MetricGoroutines, "goroutines", 61),
	}
	cfg.Churn = func(context.Context, int) error { return nil }

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := t.TempDir()
	paths, err := WriteArtifact(dir, res)
	if err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("WriteArtifact wrote %d files, want 2 (%v)", len(paths), paths)
	}

	rawCSV, err := os.ReadFile(filepath.Join(dir, SamplesFile))
	if err != nil {
		t.Fatalf("reading samples.csv: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawCSV)), "\n")
	if len(lines) != res.SampleCount+1 {
		t.Errorf("samples.csv has %d lines, want %d samples + 1 header", len(lines), res.SampleCount)
	}
	wantHeader := "elapsed_s,goroutines,table.soak_leak_unbounded"
	if lines[0] != wantHeader {
		t.Errorf("samples.csv header = %q, want %q", lines[0], wantHeader)
	}

	rawJSON, err := os.ReadFile(filepath.Join(dir, ReportFile))
	if err != nil {
		t.Fatalf("reading report.json: %v", err)
	}
	var ar ArtifactReport
	if err := json.Unmarshal(rawJSON, &ar); err != nil {
		t.Fatalf("decoding report.json: %v", err)
	}
	if ar.Seed != cfg.Seed {
		t.Errorf("report.json seed = %d, want %d", ar.Seed, cfg.Seed)
	}
	if !strings.Contains(ar.Rerun, "SOAK_SEED=12648430") {
		t.Errorf("report.json rerun line %q does not carry the seed", ar.Rerun)
	}
	if ar.Report.Pass {
		t.Error("report.json says the leaking run passed")
	}
	if len(ar.Report.Failed) != 1 || ar.Report.Failed[0] != TablePrefix+"soak_leak_unbounded" {
		t.Errorf("report.json failed list = %v, want just the leaking table", ar.Report.Failed)
	}
	if ar.SampleCount != res.SampleCount {
		t.Errorf("report.json sample_count = %d, want %d", ar.SampleCount, res.SampleCount)
	}
}

func TestWriteArtifactRejectsNilResult(t *testing.T) {
	t.Parallel()
	if _, err := WriteArtifact(t.TempDir(), nil); err == nil {
		t.Fatal("WriteArtifact(nil) succeeded")
	}
}
