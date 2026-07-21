package posture

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Factor names are stable machine keys — a caller (or the exported report's
// golden test) keys off these exact strings, so they must not change.
const (
	FactorSPOF         = "spof_resilience"
	FactorSegmentation = "segmentation"
	FactorExposedPorts = "exposed_ports"
	FactorAnomalyRate  = "anomaly_rate"
	FactorDriftHygiene = "drift_hygiene"
)

// Relative weights of the five factors in the overall score. They are the
// denominator's building blocks: the overall is the weighted mean of the
// EVALUATED factors' sub-scores, so a not-evaluated factor drops out of both
// numerator and denominator rather than contributing a phantom 100. Kept as a
// documented, testable constant set (not opaque magic) — the "never a single
// number with no factors" contract extends to how the number is built.
const (
	weightSPOF         = 30
	weightSegmentation = 25
	weightExposedPorts = 20
	weightAnomalyRate  = 15
	weightDriftHygiene = 10
)

// Sub-score penalty knobs. Each factor maps its raw metric onto a 0..100
// sub-score via a simple, documented linear penalty — deliberately legible
// arithmetic a report reader can reproduce by hand, not a tuned black box.
const (
	// exposedPortPenalty is subtracted per exposed (proto,port) across all
	// guests. 10 points each: ten wide-open inbound ports floors the factor.
	exposedPortPenalty = 10
	// anomalyPenalty scales the per-guest anomaly rate. At 50, one baseline
	// anomaly per guest halves the anomaly-rate sub-score.
	anomalyPenalty = 50.0
	// driftPenalty scales the per-node open-drift rate. At 15, ~6.6 open drift
	// findings per node floors the factor.
	driftPenalty = 15.0
)

// Factor is one named contributing dimension of the posture score. Every
// field is independently inspectable (the "never folded into overall opaquely"
// contract): Value is the raw metric, ScorePct the 0..100 sub-score it maps
// to, Weight its relative weight, and Contribution the points it actually adds
// to Overall (weight*score renormalized over the evaluated factors). A factor
// that could not be assessed carries Evaluated=false, ScorePct=NotEvaluatedScore,
// Contribution=0, and a non-empty Caveat — it still occupies a slot in the
// report, just uncounted.
type Factor struct {
	Name         string  `json:"name"`
	Detail       string  `json:"detail"`
	Caveat       string  `json:"caveat,omitempty"`
	Value        float64 `json:"value"`
	Contribution float64 `json:"contribution"`
	Weight       int     `json:"weight"`
	ScorePct     int     `json:"scorePct"`
	Evaluated    bool    `json:"evaluated"`
}

// NotEvaluatedScore is the ScorePct sentinel for a factor that was not
// assessed — distinct from a genuine 0 (assessed, and bad). A caller reading
// ScorePct must branch on Evaluated first.
const NotEvaluatedScore = -1

// Posture is the computed, render-format-agnostic report. Overall is the
// weighted mean (0..100) of the EVALUATED factors only; Qualified is the
// single "this is not a clean bill of health — at least one dimension is
// unknown or partial" honesty flag. Factors is never empty and every factor
// has a non-empty Name (the "never opaque" guarantee, asserted as a standing
// regression).
type Posture struct {
	Factors    []Factor `json:"factors"`
	ComputedAt int64    `json:"computedAt"`
	Overall    int      `json:"overall"`
	Qualified  bool     `json:"qualified"`
}

// SPOFInput carries T-1604's single-point-of-failure inventory plus the
// honesty channel: NotEvaluated is the union of failsim Impact dimensions the
// simulator could not assess for this deployment (e.g. "quorum" with no
// corosync config, "ceph"/"tunnels" absent). A non-empty NotEvaluated does not
// void the SPOF factor — the known SPOFs still lower the score — but flags it
// as a ceiling via Caveat, since a SPOF whose only blast radius is an
// unevaluated dimension would be invisible here.
type SPOFInput struct {
	Entries      []failsim.SPOFEntry
	NotEvaluated []string
}

// AnomalyFinding is the minimal projection of a findings.Finding the anomaly-
// rate factor needs — its Source (the factor counts Source=="baseline") and
// Check. Taking the projected list rather than a pre-counted int keeps the
// source-filtering logic inside this package (and under its golden test),
// decoupled from internal/findings.
type AnomalyFinding struct {
	Source string
	Check  string
}

// baselineSource is the findings Source string T-1601 stamps on its anomaly
// findings; duplicated here (not imported) to keep posture decoupled from
// internal/findings' Source type.
const baselineSource = "baseline"

// Inputs bundles the four named contributing surfaces plus the two honesty
// signals Score cannot derive on its own. Snapshot drives the two factors this
// package computes directly (segmentation and exposed ports, both read off the
// resolved firewall view); the rest arrive pre-gathered from their owning
// services. Now stamps the report (this package holds no clock).
type Inputs struct {
	Now             time.Time
	Snapshot        inventory.Snapshot
	SPOF            SPOFInput
	Findings        []AnomalyFinding
	DriftOpenCount  int
	BaselineLearned bool
}

// Score computes the posture report from in. It is pure and deterministic:
// identical Inputs always yield an identical Posture.
//
// The overall is the weighted mean of the sub-scores of the EVALUATED factors
// only — a not-evaluated factor (BaselineLearned false ⇒ cold-start anomaly
// rate) is excluded from both numerator and denominator rather than treated as
// a perfect 100, and flips Qualified. A factor computed over an incomplete
// underlying picture (SPOF with failsim NotEvaluated dimensions) still counts,
// but also flips Qualified and carries a Caveat marking the score a ceiling.
func Score(in Inputs) Posture {
	factors := []Factor{
		spofFactor(in.SPOF),
		segmentationFactor(in.Snapshot),
		exposedPortsFactor(in.Snapshot),
		anomalyRateFactor(in),
		driftHygieneFactor(in),
	}

	overall, qualified := combine(factors)

	return Posture{
		Overall:    overall,
		Qualified:  qualified,
		Factors:    factors,
		ComputedAt: in.Now.Unix(),
	}
}

// combine renormalizes the evaluated factors' weighted sub-scores into the
// 0..100 overall and stamps each evaluated factor's Contribution (its share of
// the overall). A factor is "qualifying" — it flips Posture.Qualified — when it
// is either not evaluated at all or evaluated-but-caveated. When every factor
// is unevaluated (no assessable dimension at all) the overall is 0 and the
// report is Qualified: an honest "insufficient data", never a default 100.
func combine(factors []Factor) (overall int, qualified bool) {
	totalWeight := 0
	weightedSum := 0
	for _, f := range factors {
		if f.Caveat != "" {
			qualified = true
		}
		if !f.Evaluated {
			continue
		}
		totalWeight += f.Weight
		weightedSum += f.Weight * f.ScorePct
	}
	if totalWeight == 0 {
		return 0, true
	}
	// Stamp each evaluated factor's contribution to the (renormalized) overall.
	for i := range factors {
		if !factors[i].Evaluated {
			continue
		}
		factors[i].Contribution = float64(factors[i].Weight*factors[i].ScorePct) / float64(totalWeight)
	}
	overall = int(math.Round(float64(weightedSum) / float64(totalWeight)))
	return overall, qualified
}

// spofFactor scores resilience from T-1604's SPOF inventory, reusing
// failsim.Score (100 minus each SPOF's severity weight) verbatim so the posture
// tile and the failsim dashboard tile can never disagree on what a SPOF costs.
// A non-empty NotEvaluated does not void the factor but caveats it: the known
// SPOFs are real, but a SPOF whose only impact is an unevaluated dimension
// would be missing, so the score is a ceiling.
func spofFactor(in SPOFInput) Factor {
	score := failsim.Score(in.Entries)
	f := Factor{
		Name:      FactorSPOF,
		Weight:    weightSPOF,
		Value:     float64(len(in.Entries)),
		ScorePct:  score,
		Evaluated: true,
		Detail: fmt.Sprintf("%d single point(s) of failure with known blast radius; resilience %d/100",
			len(in.Entries), score),
	}
	if len(in.NotEvaluated) > 0 {
		dims := sortedUnique(in.NotEvaluated)
		f.Caveat = fmt.Sprintf("failure simulation could not assess: %v — this resilience score is a ceiling, real posture may be lower", dims)
	}
	return f
}

// segmentationFactor scores the fraction of guests carrying an applied T-1602
// microsegmentation policy. "Applied" means a rule bearing microseg's marker
// comment is actually present in the guest's resolved firewall view — a
// proposal that was only dry-run never wrote such a rule, so it counts
// unsegmented (the "only applied coverage counts, never aspirational"
// contract). Value is the UNSEGMENTED fraction (higher = worse), so it reads
// the same direction as the other factors' "raw badness" values.
func segmentationFactor(snap inventory.Snapshot) Factor {
	total, segmented := segmentationCounts(snap)
	f := Factor{
		Name:      FactorSegmentation,
		Weight:    weightSegmentation,
		Evaluated: true,
	}
	if total == 0 {
		f.ScorePct = 100
		f.Value = 0
		f.Detail = "no guests present; nothing to segment"
		return f
	}
	f.Value = float64(total-segmented) / float64(total)
	f.ScorePct = int(math.Round(100 * float64(segmented) / float64(total)))
	f.Detail = fmt.Sprintf("%d of %d guest(s) carry an applied microsegmentation policy; %d unsegmented",
		segmented, total, total-segmented)
	return f
}

// exposedPortsFactor counts guest-scope inbound rules that permit traffic from
// any source with no narrower rule ahead of them, across every guest's resolved
// firewall view. Value is the exposed-port count.
func exposedPortsFactor(snap inventory.Snapshot) Factor {
	exposed := exposedPortCount(snap)
	score := 100 - exposedPortPenalty*exposed
	if score < 0 {
		score = 0
	}
	return Factor{
		Name:      FactorExposedPorts,
		Weight:    weightExposedPorts,
		Value:     float64(exposed),
		ScorePct:  score,
		Evaluated: true,
		Detail:    fmt.Sprintf("%d guest port(s) reachable from any source (0.0.0.0/0 or ::/0) with no narrower rule ahead", exposed),
	}
}

// anomalyRateFactor scores T-1601's baseline-anomaly finding count normalized
// per guest. HONESTY: a cold-start baseline (no learned profiles) makes "zero
// anomalies" meaningless — it means "we have never looked", not "nothing is
// wrong" — so the factor is marked NOT EVALUATED and excluded from the overall,
// never allowed to contribute a phantom perfect score.
func anomalyRateFactor(in Inputs) Factor {
	anomalies := 0
	for _, f := range in.Findings {
		if f.Source == baselineSource {
			anomalies++
		}
	}
	f := Factor{
		Name:   FactorAnomalyRate,
		Weight: weightAnomalyRate,
		Value:  float64(anomalies),
	}
	if !in.BaselineLearned {
		f.Evaluated = false
		f.ScorePct = NotEvaluatedScore
		f.Detail = "no learned baseline profiles yet (cold start)"
		f.Caveat = "anomaly rate not evaluated: baselines are still learning, so an absence of anomaly findings cannot be read as healthy"
		return f
	}
	guests := guestCount(in.Snapshot)
	denom := guests
	if denom == 0 {
		denom = 1
	}
	rate := float64(anomalies) / float64(denom)
	f.Value = rate
	score := 100 - int(math.Round(anomalyPenalty*rate))
	if score < 0 {
		score = 0
	}
	f.ScorePct = score
	f.Evaluated = true
	f.Detail = fmt.Sprintf("%d baseline anomaly finding(s) across %d guest(s) (%.3f per guest)", anomalies, guests, rate)
	return f
}

// driftHygieneFactor scores the existing open-drift finding count normalized
// per cluster node. No new detection logic — it consumes internal/drift's
// count as gathered by the caller.
func driftHygieneFactor(in Inputs) Factor {
	nodes := nodeCount(in.Snapshot)
	denom := nodes
	if denom == 0 {
		denom = 1
	}
	rate := float64(in.DriftOpenCount) / float64(denom)
	score := 100 - int(math.Round(driftPenalty*rate))
	if score < 0 {
		score = 0
	}
	return Factor{
		Name:      FactorDriftHygiene,
		Weight:    weightDriftHygiene,
		Value:     rate,
		ScorePct:  score,
		Evaluated: true,
		Detail:    fmt.Sprintf("%d open drift finding(s) across %d node(s) (%.3f per node)", in.DriftOpenCount, nodes, rate),
	}
}

func guestCount(snap inventory.Snapshot) int {
	n := 0
	for _, e := range snap.All() {
		if e.GetRef().Kind == inventory.KindGuest {
			n++
		}
	}
	return n
}

func nodeCount(snap inventory.Snapshot) int {
	n := 0
	for _, e := range snap.All() {
		if e.GetRef().Kind == inventory.KindNode {
			n++
		}
	}
	return n
}

func sortedUnique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
