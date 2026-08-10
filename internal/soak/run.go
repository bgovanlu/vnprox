package soak

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// MinSamplesPerRun is the floor Run enforces on every sampler: a run that
// read a metric once cannot say anything about its trend, and must not be
// allowed to report "no leak found". T-2504 AC5 states the same bar from
// the outside — a 60-second run has to exercise every sampler at least
// twice — so it is enforced here rather than left to the caller.
const MinSamplesPerRun = 2

// ErrChurnDead is returned when every churn tick failed. A soak whose churn
// generator never actually drove the daemon measures an idle process, which
// is the one result that must never be reported as a pass.
var ErrChurnDead = errors.New("soak: every churn tick failed; the run measured an idle daemon")

// Config configures one soak run.
type Config struct {
	// Churn drives the system under test. Called on ChurnInterval with a
	// monotonically increasing tick number. Individual errors are counted
	// and logged, not fatal (a mock PVE server under churn legitimately
	// rejects some operations); every tick failing is fatal (ErrChurnDead).
	// Nil means no churn — useful only for testing this package itself.
	Churn func(ctx context.Context, tick int) error
	// Logger receives one structured line per sample. Required to be
	// non-nil by Run only in the sense that nil falls back to
	// slog.Default(); no fmt printing happens anywhere in this package.
	Logger   *slog.Logger
	Samplers []Sampler
	Policy   Policy
	// Seed is the churn generator's seed. This package does not use it —
	// the caller builds Churn from it — but it is recorded in the Result
	// and the artifact so a failing run is reproducible (T-2504 AC4).
	Seed     uint64
	Duration time.Duration
	// Interval is the sampling interval. Every sampler is read on every
	// tick, so the series are aligned by construction.
	Interval time.Duration
	// ChurnInterval defaults to Interval when zero.
	ChurnInterval time.Duration
}

// Result is everything one run produced: the raw series, the churn
// bookkeeping, and the verdict.
type Result struct {
	StartedAt     time.Time     `json:"started_at"`
	EndedAt       time.Time     `json:"ended_at"`
	Series        []Series      `json:"-"`
	Report        Report        `json:"report"`
	Seed          uint64        `json:"seed"`
	Duration      time.Duration `json:"-"`
	Interval      time.Duration `json:"-"`
	ChurnInterval time.Duration `json:"-"`
	SampleCount   int           `json:"sample_count"`
	ChurnTicks    int           `json:"churn_ticks"`
	ChurnErrors   int           `json:"churn_errors"`
}

// Run samples every configured sampler on Config.Interval for
// Config.Duration while Config.Churn drives the daemon, then analyzes the
// resulting series against Config.Policy.
//
// It returns a non-nil *Result whenever sampling produced anything at all,
// even when the returned error is non-nil — the caller still wants to write
// the artifact for a failed or inconclusive run. The gate verdict itself is
// Result.Report; a passing analysis with a failing report is not an error
// from Run's point of view, so callers must check both:
//
//	res, err := soak.Run(ctx, cfg)
//	if res != nil { _ = soak.WriteArtifact(dir, res) }
//	if err != nil { ... }
//	if err := res.Report.Err(); err != nil { ... }
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if len(cfg.Samplers) == 0 {
		return nil, errors.New("soak: at least one sampler is required")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("soak: duration must be positive, got %s", cfg.Duration)
	}
	if cfg.Interval <= 0 {
		return nil, fmt.Errorf("soak: sample interval must be positive, got %s", cfg.Interval)
	}
	churnInterval := cfg.ChurnInterval
	if churnInterval <= 0 {
		churnInterval = cfg.Interval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	seen := make(map[string]bool, len(cfg.Samplers))
	for _, s := range cfg.Samplers {
		if seen[s.Name()] {
			return nil, fmt.Errorf("soak: duplicate sampler name %q; series would collide in the artifact", s.Name())
		}
		seen[s.Name()] = true
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var churnTicks, churnErrors atomic.Int64
	churnDone := make(chan struct{})
	go func() {
		defer close(churnDone)
		if cfg.Churn == nil {
			return
		}
		ticker := time.NewTicker(churnInterval)
		defer ticker.Stop()
		for tick := 0; ; tick++ {
			churnTicks.Add(1)
			if err := cfg.Churn(runCtx, tick); err != nil {
				if runCtx.Err() != nil {
					// Shutting down: an in-flight churn operation failing
					// because the run ended is not a churn failure.
					churnTicks.Add(-1)
					return
				}
				churnErrors.Add(1)
				logger.Warn("soak: churn tick failed", "tick", tick, "error", err)
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	series := make([]Series, len(cfg.Samplers))
	for i, s := range cfg.Samplers {
		series[i] = Series{Metric: s.Name(), Unit: s.Unit()}
	}

	start := time.Now()
	deadline := start.Add(cfg.Duration)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	var sampleErr error
	samples := 0
sampleLoop:
	for {
		now := time.Now()
		elapsedMin := now.Sub(start).Minutes()
		attrs := make([]any, 0, 2*len(cfg.Samplers)+1)
		attrs = append(attrs, "elapsed_s", int(now.Sub(start).Seconds()))
		for i, s := range cfg.Samplers {
			v, err := s.Sample(runCtx)
			if err != nil {
				sampleErr = fmt.Errorf("sampling %s: %w", s.Name(), err)
				break sampleLoop
			}
			series[i].Elapsed = append(series[i].Elapsed, elapsedMin)
			series[i].Values = append(series[i].Values, v)
			if !isTableMetric(s.Name()) {
				attrs = append(attrs, s.Name(), v)
			}
		}
		samples++
		logger.Info("soak: sample", attrs...)

		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-runCtx.Done():
			sampleErr = runCtx.Err()
			break sampleLoop
		case <-ticker.C:
		}
	}

	cancel()
	<-churnDone

	res := &Result{
		Seed:          cfg.Seed,
		StartedAt:     start,
		EndedAt:       time.Now(),
		Duration:      cfg.Duration,
		Interval:      cfg.Interval,
		ChurnInterval: churnInterval,
		Series:        series,
		SampleCount:   samples,
		ChurnTicks:    int(churnTicks.Load()),
		ChurnErrors:   int(churnErrors.Load()),
	}
	if sampleErr != nil {
		return res, sampleErr
	}
	if samples < MinSamplesPerRun {
		return res, fmt.Errorf("soak: only %d sample(s) taken over %s at a %s interval; need at least %d for a trend",
			samples, cfg.Duration, cfg.Interval, MinSamplesPerRun)
	}
	if cfg.Churn != nil && res.ChurnTicks > 0 && res.ChurnErrors == res.ChurnTicks {
		return res, fmt.Errorf("%w (%d/%d ticks failed)", ErrChurnDead, res.ChurnErrors, res.ChurnTicks)
	}

	report, err := Analyze(series, cfg.Policy)
	if err != nil {
		return res, err
	}
	res.Report = report
	return res, nil
}

func isTableMetric(name string) bool {
	return len(name) > len(TablePrefix) && name[:len(TablePrefix)] == TablePrefix
}
