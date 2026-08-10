package compliance

import (
	"fmt"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// evaluate.go turns a Profile plus a set of Inputs into a Report. It is pure
// and deterministic: identical Inputs always yield an identical Report, so
// the whole safety property is unit-testable without a daemon.

// FindingRef is the minimal projection of a findings.Finding this package
// needs — deliberately a projection rather than an import, the same
// decoupling internal/posture.AnomalyFinding already uses, so the evaluator
// can be driven from a live stream or from reconstructed history without
// caring which.
type FindingRef struct {
	ID    string
	Check string
	// Severity is "error"/"warning"/"info", or "" when it is NOT KNOWN —
	// which is the case for a finding reconstructed from the retained
	// transition history, since finding_events records the transition, not
	// the severity. An unknown severity is treated as meeting every
	// threshold: a control fails rather than passes on evidence we cannot
	// grade.
	Severity string
	// Acked reports whether the finding carries an active T-2402
	// acknowledgement. An acknowledged finding STILL fails its control —
	// acknowledgement is triage, not remediation, and a compliance report
	// that could be cleared by acking would be worthless. The ack is
	// reported alongside instead.
	Acked bool
}

// PolicyRuleRef is the minimal projection of one installed T-2601 policy
// rule plus its runtime bookkeeping (change.PolicyRuleStatus).
type PolicyRuleRef struct {
	ID   string
	Tags []string
	// ProbablyMisconfigured is T-2601's own "this rule has been evaluated
	// enough times, over a long enough window, and has never matched
	// anything". A rule in that state is NOT evidence: it is reported as
	// not evaluated, never as a pass.
	ProbablyMisconfigured bool
	EvalCount             int64
	MatchCount            int64
	LastMatchedAt         int64
}

// PolicyState is the cluster's installed policy set as evidence.
type PolicyState struct {
	Rules    []PolicyRuleRef
	Revision int64
	// Configured is false when this daemon has no policy store wired
	// (change.ErrPolicyNotConfigured). Every policy evidence item is then
	// not evaluated — never satisfied by default.
	Configured bool
}

// Inputs is everything the evaluator reads. Nothing here is fetched by this
// package: the caller gathers each surface from its owning service, the same
// composition-root discipline internal/posture.Inputs uses.
type Inputs struct {
	ProductVersion string
	// CheckUniverse names where KnownChecks came from, carried into the
	// report so the list's completeness is legible.
	CheckUniverse string
	// Findings is the open findings the report is evidenced by.
	Findings []FindingRef
	// KnownChecks is the check universe the unmapped-check list is
	// computed against — findings.AllCheckNames() in production. Checks
	// observed in Findings are unioned in, so a check the catalog does not
	// know about still cannot hide.
	KnownChecks []string
	// Policy is T-2601's installed rule set, projected.
	Policy PolicyState
	Now    time.Time
	// AsOf is zero for a live report. A non-zero AsOf marks the evidence
	// as reconstructed from retained history, which weakens it in ways the
	// report states rather than hides.
	AsOf time.Time
	// Posture is T-1607's latest score; PostureOK is false when none has
	// been computed yet (a freshly started daemon), which makes every
	// posture evidence item not evaluated rather than absent.
	Posture posture.Posture
	// PostureOK is false when no posture score has been computed yet.
	PostureOK bool
}

// Evaluate produces the report for p against in.
func Evaluate(p Profile, in Inputs) Report {
	historical := !in.AsOf.IsZero()

	byCheck := map[string][]FindingRef{}
	observed := make([]string, 0, len(in.Findings))
	for _, f := range in.Findings {
		byCheck[f.Check] = append(byCheck[f.Check], f)
		observed = append(observed, f.Check)
	}

	controls := make([]ControlResult, 0, len(p.Controls))
	for _, c := range p.Controls {
		controls = append(controls, evaluateControl(c, in, byCheck, historical))
	}

	rep := Report{
		ProductVersion: in.ProductVersion,
		ProfileID:      p.ID,
		ProfileTitle:   p.Title,
		ProfileVersion: p.Version,
		Notice:         p.Notice,
		GeneratedAt:    in.Now.Unix(),
		Summary:        summarize(controls),
		Controls:       controls,
		UnmappedChecks: unmappedChecks(p, in.KnownChecks, observed),
		CheckUniverse:  in.CheckUniverse,
	}
	if rep.CheckUniverse == "" {
		rep.CheckUniverse = "checks observed in this report's evidence only"
	}
	if historical {
		rep.AsOf = in.AsOf.Unix()
		rep.Caveats = append(rep.Caveats,
			fmt.Sprintf("This report describes %s, reconstructed from retained finding-transition history. "+
				"That history records WHICH findings were open, not how severe they were, so every open finding at that "+
				"moment is treated as failing its control — a historical report can be stricter than the live one was, "+
				"never more lenient.", in.AsOf.UTC().Format(time.RFC3339)),
			"Policy evidence is not evaluated in a historical report: T-2601's per-rule match bookkeeping is current-state "+
				"only, so whether a rule was actually guarding anything on that date cannot be established.")
	}
	if !in.PostureOK {
		rep.Caveats = append(rep.Caveats,
			"No posture score was available, so every posture-factor evidence item is reported as not evaluated.")
	}
	if !in.Policy.Configured && !historical {
		rep.Caveats = append(rep.Caveats,
			"No policy store is configured on this daemon, so every policy evidence item is reported as not evaluated.")
	}
	if len(rep.UnmappedChecks) > 0 {
		rep.Caveats = append(rep.Caveats, fmt.Sprintf(
			"%d check(s) this build can emit are mapped by no control in this profile; they are listed in full below and "+
				"contribute to no control's status.", len(rep.UnmappedChecks)))
	}
	return rep
}

func evaluateControl(c Control, in Inputs, byCheck map[string][]FindingRef, historical bool) ControlResult {
	out := ControlResult{ID: c.ID, Title: c.Title, Statement: c.Statement}

	// THE SAFETY PROPERTY, in one place: no mapped evidence, no pass.
	if len(c.Evidence) == 0 {
		out.Stat = StatusUnmapped
		out.UnmappedReason = c.UnmappedReason
		return out
	}

	for _, e := range c.Evidence {
		out.Evidence = append(out.Evidence, evaluateEvidence(e, in, byCheck, historical))
	}

	anyUnsatisfied, anyUnevaluated := false, false
	for _, r := range out.Evidence {
		switch r.Stat {
		case EvidenceUnsatisfied:
			anyUnsatisfied = true
		case EvidenceNotEvaluated:
			anyUnevaluated = true
		case EvidenceSatisfied:
		}
	}
	switch {
	case anyUnsatisfied:
		out.Stat = StatusFail
	case anyUnevaluated:
		// A control is only asserted when ALL of its mapped evidence was
		// evaluated. A partially-evaluated control that reported `pass`
		// would be asserting more than it checked.
		out.Stat = StatusNotEvaluated
	default:
		out.Stat = StatusPass
	}
	return out
}

func evaluateEvidence(e Evidence, in Inputs, byCheck map[string][]FindingRef, historical bool) EvidenceResult {
	r := EvidenceResult{Kind: e.Kind, Name: e.Name(), Note: e.Note}
	switch e.Kind {
	case EvidenceCheck:
		evaluateCheckEvidence(&r, e, byCheck[e.Check])
	case EvidencePosture:
		evaluatePostureEvidence(&r, e, in)
	case EvidencePolicy:
		evaluatePolicyEvidence(&r, e, in, historical)
	default:
		// Unreachable for a validated profile; reported rather than
		// assumed, since an unknown kind must not be able to pass.
		r.Stat = EvidenceNotEvaluated
		r.Detail = fmt.Sprintf("evidence kind %q is not understood by this build", e.Kind)
	}
	return r
}

func evaluateCheckEvidence(r *EvidenceResult, e Evidence, open []FindingRef) {
	failAt := e.FailAt
	if failAt == "" {
		failAt = DefaultFailAt
	}
	threshold := knownSeverities[failAt]

	var failing, acked []string
	for _, f := range open {
		// An unknown severity (reconstructed history) counts as meeting
		// every threshold: unevaluable evidence must not read as clean.
		if f.Severity != "" && knownSeverities[f.Severity] < threshold {
			continue
		}
		failing = append(failing, f.ID)
		if f.Acked {
			acked = append(acked, f.ID)
		}
	}

	if len(failing) == 0 {
		r.Stat = EvidenceSatisfied
		r.Detail = fmt.Sprintf("no open finding for check %q at severity %s or above", e.Check, failAt)
		return
	}
	r.Stat = EvidenceUnsatisfied
	r.Refs = sortedUnique(failing)
	r.Detail = fmt.Sprintf("%d open finding(s) for check %q at severity %s or above", len(failing), e.Check, failAt)
	if len(acked) > 0 {
		r.Detail += fmt.Sprintf("; %d of them carry an operator acknowledgement, which is triage and does not clear the control", len(acked))
	}
}

func evaluatePostureEvidence(r *EvidenceResult, e Evidence, in Inputs) {
	if !in.PostureOK {
		r.Stat = EvidenceNotEvaluated
		r.Detail = "no posture score has been computed on this daemon yet"
		return
	}
	for _, f := range in.Posture.Factors {
		if f.Name != e.Factor {
			continue
		}
		// T-1607's honesty channel, carried through rather than flattened:
		// a factor posture could not assess is not evidence here either.
		if !f.Evaluated || f.ScorePct == posture.NotEvaluatedScore {
			r.Stat = EvidenceNotEvaluated
			r.Detail = fmt.Sprintf("posture factor %q was not evaluated: %s", e.Factor, firstNonEmpty(f.Caveat, f.Detail))
			return
		}
		if f.ScorePct < e.MinScore {
			r.Stat = EvidenceUnsatisfied
			r.Detail = fmt.Sprintf("posture factor %q scored %d/100, below this profile's minimum of %d: %s",
				e.Factor, f.ScorePct, e.MinScore, f.Detail)
			return
		}
		r.Stat = EvidenceSatisfied
		r.Detail = fmt.Sprintf("posture factor %q scored %d/100, at or above this profile's minimum of %d",
			e.Factor, f.ScorePct, e.MinScore)
		if f.Caveat != "" {
			r.Detail += "; the factor is caveated: " + f.Caveat
		}
		return
	}
	r.Stat = EvidenceNotEvaluated
	r.Detail = fmt.Sprintf("this build's posture score reports no factor named %q", e.Factor)
}

func evaluatePolicyEvidence(r *EvidenceResult, e Evidence, in Inputs, historical bool) {
	if historical {
		r.Stat = EvidenceNotEvaluated
		r.Detail = "policy evidence is not reconstructed for a historical report: T-2601's per-rule match bookkeeping is current-state only"
		return
	}
	if !in.Policy.Configured {
		r.Stat = EvidenceNotEvaluated
		r.Detail = "no policy store is configured on this daemon, so no rule can be cited as evidence"
		return
	}

	matched := make([]PolicyRuleRef, 0, len(in.Policy.Rules))
	for _, rule := range in.Policy.Rules {
		if e.Rule != "" && rule.ID == e.Rule {
			matched = append(matched, rule)
			continue
		}
		if e.Tag != "" && hasTag(rule.Tags, e.Tag) {
			matched = append(matched, rule)
		}
	}
	if len(matched) == 0 {
		r.Stat = EvidenceNotEvaluated
		if e.Rule != "" {
			r.Detail = fmt.Sprintf("no policy rule %q is installed on this cluster", e.Rule)
		} else {
			r.Detail = fmt.Sprintf("no installed policy rule carries the tag %q", e.Tag)
		}
		return
	}

	var misconfigured []string
	for _, rule := range matched {
		r.Refs = append(r.Refs, rule.ID)
		if rule.ProbablyMisconfigured {
			misconfigured = append(misconfigured, rule.ID)
		}
	}
	r.Refs = sortedUnique(r.Refs)
	if len(misconfigured) > 0 {
		// T-2601's author's instruction, honoured verbatim: do not render
		// an unmatched rule as `pass`. A rule that has never matched an op
		// is not evidence that anything was guarded.
		r.Stat = EvidenceNotEvaluated
		r.Detail = fmt.Sprintf("%d installed rule(s) (%s) have never matched an op and are reported by the policy engine as probably misconfigured; a rule that guards nothing is not evidence",
			len(misconfigured), strings.Join(sortedUnique(misconfigured), ", "))
		return
	}
	r.Stat = EvidenceSatisfied
	r.Detail = fmt.Sprintf("%d installed policy rule(s) (%s), all of which the policy engine reports as actively matching",
		len(matched), strings.Join(r.Refs, ", "))
}

// unmappedChecks is the check universe minus the checks this profile maps.
func unmappedChecks(p Profile, known, observed []string) []string {
	mapped := map[string]bool{}
	for _, c := range p.MappedChecks() {
		mapped[c] = true
	}
	universe := sortedUnique(append(append([]string(nil), known...), observed...))
	out := make([]string, 0, len(universe))
	for _, name := range universe {
		if !mapped[name] {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
