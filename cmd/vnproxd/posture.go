// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/posture"
	"github.com/bgovanlu/vnprox/internal/store"
)

// posture.go wires T-1607's network posture score & report: a scheduled
// computation job (supervised goroutine, default daily — alongside T-1606's
// rollup job pattern) that folds T-1604's SPOF inventory, T-1601's anomaly
// findings, T-1602's applied segmentation, internal/fw's resolved firewall
// view, and internal/drift's open findings into one explainable score, persists
// it to the bounded posture_scores table, and the read adapter GET /posture /
// /posture/history / /export/posture serve from. Nothing here re-implements a
// factor: each dimension is gathered from its owning surface, so the posture
// tile can never silently disagree with the surface it summarizes.

// postureDefaultInterval is the scheduled computation cadence (daily), matching
// T-1606's capacity rollup.
const postureDefaultInterval = 24 * time.Hour

// postureFindingsSource is the seam onto the unified findings stream the
// posture job counts (findings.Engine): baseline-anomaly findings for the
// anomaly-rate factor and drift findings for the drift-hygiene factor, both
// read from the same live stream the API serves.
type postureFindingsSource interface {
	Findings() []findings.Finding
}

// postureBaselineCounter reports how many baseline profiles have been learned;
// zero means cold-start, which makes the anomaly-rate factor honestly
// not-evaluated rather than a false "no anomalies = healthy".
type postureBaselineCounter interface {
	Count(ctx context.Context) (int64, error)
}

// postureJob computes and persists one posture score per scheduled tick. It is
// the composition-root glue between the pure internal/posture scorer and the
// live daemon services; internal/posture itself imports none of them.
type postureJob struct {
	graph     *inventory.Graph
	failsim   *failsimAdapter
	findings  postureFindingsSource
	baselines postureBaselineCounter
	repo      *store.PostureScoreRepo
	now       func() time.Time
	logger    *slog.Logger
}

// gather assembles internal/posture.Inputs from the live services. The SPOF
// inventory and its not-evaluated dimensions come from the same failsim adapter
// GET /failsim/spof-score uses; the anomaly and drift counts from the unified
// findings stream; segmentation and exposed ports are computed by the scorer
// itself off the inventory snapshot.
func (j *postureJob) gather(ctx context.Context) posture.Inputs {
	snap := j.graph.Snapshot()

	in := posture.Inputs{
		Snapshot:        snap,
		Now:             j.now(),
		BaselineLearned: j.baselineLearned(ctx),
	}

	if j.failsim != nil {
		fsIn, _ := j.failsim.input()
		in.SPOF = posture.SPOFInput{
			Entries:      failsim.Inventory(fsIn),
			NotEvaluated: spofNotEvaluated(fsIn),
		}
	}

	if j.findings != nil {
		for _, f := range j.findings.Findings() {
			switch f.Source {
			case findings.SourceBaseline:
				in.Findings = append(in.Findings, posture.AnomalyFinding{Source: string(f.Source), Check: f.Check})
			case findings.SourceDrift:
				in.DriftOpenCount++
			}
		}
	}
	return in
}

func (j *postureJob) baselineLearned(ctx context.Context) bool {
	if j.baselines == nil {
		return false
	}
	n, err := j.baselines.Count(ctx)
	if err != nil {
		j.logger.Warn("posture: counting baseline profiles", "error", err)
		return false
	}
	return n > 0
}

// spofNotEvaluated mirrors internal/failsim's own honesty preconditions
// (impact.go/honesty.go): a deployment-level impact dimension the simulator
// cannot assess because its side-table is absent. Surfaced so the SPOF factor
// caveats itself ("this resilience score is a ceiling") rather than implying a
// clean bill of health for a dimension that was never checked.
func spofNotEvaluated(in failsim.Input) []string {
	var dims []string
	if in.Corosync == nil {
		dims = append(dims, failsim.DimQuorum)
	}
	if in.Ceph == nil || (in.Ceph.PublicNetwork == "" && in.Ceph.ClusterNetwork == "") {
		dims = append(dims, failsim.DimCeph)
	}
	if len(in.Tunnels) == 0 {
		dims = append(dims, failsim.DimTunnels)
	}
	return dims
}

// computeAndStore runs one scoring pass and persists it, idempotently per UTC
// day: it clears any prior row for today's day before inserting the fresh one,
// so a re-run within the same day replaces rather than duplicates (T-1607 AC5).
func (j *postureJob) computeAndStore(ctx context.Context) error {
	in := j.gather(ctx)
	p := posture.Score(in)

	factorsJSON, err := json.Marshal(p.Factors)
	if err != nil {
		return fmt.Errorf("cmd/vnproxd: marshaling posture factors: %w", err)
	}

	dayStart := j.now().UTC().Truncate(24 * time.Hour)
	if _, err := j.repo.DeleteInRange(ctx, dayStart.Unix(), dayStart.Add(24*time.Hour).Unix()); err != nil {
		return fmt.Errorf("cmd/vnproxd: clearing prior posture score for the day: %w", err)
	}
	if _, err := j.repo.Insert(ctx, store.PostureScore{
		ComputedAt:  p.ComputedAt,
		Overall:     p.Overall,
		Qualified:   p.Qualified,
		FactorsJSON: string(factorsJSON),
	}); err != nil {
		return fmt.Errorf("cmd/vnproxd: inserting posture score: %w", err)
	}
	return nil
}

// RunLoop runs one computation immediately, then every interval until ctx is
// cancelled — the run-once-then-tick shape every other scheduled job in this
// daemon uses. A failed pass is logged and the loop continues; it never takes
// down the process.
func (j *postureJob) RunLoop(ctx context.Context, interval time.Duration) error {
	if err := j.computeAndStore(ctx); err != nil {
		j.logger.Error("posture: computation pass failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := j.computeAndStore(ctx); err != nil {
				j.logger.Error("posture: computation pass failed", "error", err)
			}
		}
	}
}

// postureReadAdapter implements api.PostureService over the posture_scores
// repo, deserializing the stored factors_json back into posture.Posture.
type postureReadAdapter struct {
	repo *store.PostureScoreRepo
}

func (a postureReadAdapter) Latest(ctx context.Context) (posture.Posture, bool, error) {
	row, err := a.repo.Latest(ctx)
	if err != nil {
		if err == store.ErrNotFound {
			return posture.Posture{}, false, nil
		}
		return posture.Posture{}, false, err
	}
	p, err := postureFromRow(row)
	if err != nil {
		return posture.Posture{}, false, err
	}
	return p, true, nil
}

func (a postureReadAdapter) History(ctx context.Context, limit int) ([]posture.Posture, error) {
	rows, err := a.repo.History(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]posture.Posture, 0, len(rows))
	for _, row := range rows {
		p, convErr := postureFromRow(row)
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, p)
	}
	return out, nil
}

func postureFromRow(row store.PostureScore) (posture.Posture, error) {
	var factors []posture.Factor
	if row.FactorsJSON != "" {
		if err := json.Unmarshal([]byte(row.FactorsJSON), &factors); err != nil {
			return posture.Posture{}, fmt.Errorf("cmd/vnproxd: unmarshaling posture factors: %w", err)
		}
	}
	return posture.Posture{
		Overall:    row.Overall,
		Qualified:  row.Qualified,
		Factors:    factors,
		ComputedAt: row.ComputedAt,
	}, nil
}

// setupPosture wires T-1607's posture job and read adapter. failsimSvc/
// findingsEngine/baselineProfiles are the live surfaces the job folds; a nil
// graph (degraded startup) is tolerated by the scorer (empty snapshot ⇒ trivial
// factors). Returns the scheduled computation actor, the prune actor, and the
// read adapter for the API router.
func setupPosture(graph *inventory.Graph, failsimSvc *failsimAdapter, findingsSrc postureFindingsSource, baselines postureBaselineCounter, db *store.DB, logger *slog.Logger) (compute, prune func(context.Context) error, read postureReadAdapter) {
	repo := store.NewPostureScoreRepo(db)
	job := &postureJob{
		graph:     graph,
		failsim:   failsimSvc,
		findings:  findingsSrc,
		baselines: baselines,
		repo:      repo,
		now:       time.Now,
		logger:    logger,
	}
	compute = func(ctx context.Context) error {
		return job.RunLoop(ctx, postureDefaultInterval)
	}
	prune = func(ctx context.Context) error {
		return repo.RunPruneLoop(ctx, posturePruneInterval, store.DefaultPostureKeepCount, store.DefaultPostureRetentionDays, func(err error) {
			logger.Error("store: posture_scores prune failed", "error", err)
		})
	}
	read = postureReadAdapter{repo: repo}
	return compute, prune, read
}

// posturePruneInterval is how often the posture_scores retention prune runs.
// Daily is ample for a table that gains one row per day.
const posturePruneInterval = 12 * time.Hour
