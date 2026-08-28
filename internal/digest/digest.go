// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"context"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/posture"
)

// PostureNotScored is Run.PostureOverall's "this digest carried no posture
// score" sentinel — the same value internal/store.DigestPostureNotScored
// persists, declared here so this package does not import internal/store (the
// same decoupling internal/findings' AlertRule/AlertRuleProvider seam has).
//
// It is a sentinel rather than a nil pointer because the distinction it makes
// is load-bearing: a stored 0 means "scored, and the score was zero", and
// treating that as "no score" (or the reverse) is how a first digest ends up
// reporting a delta against nothing.
const PostureNotScored = -1

// CheckScheduledDigest is the check name stamped on the synthetic Finding a
// digest is delivered as.
//
// It is deliberately NOT in findings.AllCheckNames(): nothing evaluates it, no
// producer can raise it, and it never enters the findings stream — it exists
// only as the envelope T-2407's delivery path carries. This is the same
// treatment findings.DigestFinding's own "alert_digest" already gets, for the
// same reason: a compliance profile asking "which checks does no control map"
// must not be told about an envelope.
const CheckScheduledDigest = "scheduled_digest"

// Schedule is this package's view of one digest schedule — decoupled from
// internal/store.DigestSchedule the same way findings.AlertRule is decoupled
// from store.AlertRule.
//
// RuleIDs empty means "every enabled alert rule that matches", which is
// T-2407's ordinary fan-out.
type Schedule struct {
	RuleIDs []string
	Every   time.Duration
	Enabled bool
}

// Run is the summary of a digest that was generated: the durable baseline the
// NEXT digest computes its deltas against, and the record of what happened to
// this one.
type Run struct {
	Status         string
	Detail         string
	PeriodStart    int64
	PeriodEnd      int64
	GeneratedAt    int64
	PostureOverall int64
	Opened         int64
	Closed         int64
	Drift          int64
	Capacity       int64
	Quiet          bool
}

// Run status vocabulary, mirroring internal/store's.
const (
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
)

// Transition is one finding transition inside the reported window — this
// package's view of a finding_events row.
type Transition struct {
	FindingID  string
	Transition string
	At         int64
}

// Store is the digest's own durable state.
//
// Schedule is read on EVERY Tick rather than cached at construction. That is
// the whole of AC5: caching it here would make a schedule change require a
// daemon restart, which is exactly the property the card forbids.
type Store interface {
	Schedule(ctx context.Context) (Schedule, bool, error)
	LatestRun(ctx context.Context) (Run, bool, error)
	RecordRun(ctx context.Context, r Run) error
}

// PostureSource is the posture read model — the same seam GET /posture serves
// from, so the digest's score can never disagree with the score the UI shows.
type PostureSource interface {
	Latest(ctx context.Context) (posture.Posture, bool, error)
}

// FindingsSource is the live unified findings stream — the same one GET
// /findings serves. Capacity projections crossing the horizon and unresolved
// drift are both already in it (source "capacity" and source "drift"
// respectively), so the digest reads them from there rather than opening a
// second path to internal/capacity and internal/drift and risking a digest
// that disagrees with the findings page it summarises.
type FindingsSource interface {
	Findings() []findings.Finding
}

// HistorySource is the retained finding-transition history — the same
// finding_events feed GET /history/events reads. It is what makes "opened and
// closed IN THE PERIOD" answerable at all: the live stream only knows what is
// true now.
type HistorySource interface {
	Transitions(ctx context.Context, from, to int64) ([]Transition, error)
}

// Notifier is T-2407's delivery path, narrowed to the one method this package
// uses. *findings.WebhookNotifier satisfies it directly.
type Notifier interface {
	Notify(ctx context.Context, f findings.Finding, kind findings.TransitionKind) error
}

// build assembles the report for the window ending at now.
//
// prev is the previous digest's run, or nil for a first-ever digest. The
// window's START is prev's own end — that is what "deltas against the previous
// digest, not against an arbitrary window" means structurally: consecutive
// digests abut exactly, and nothing falls between two of them unreported.
//
// With no previous digest there is no such boundary, so the window falls back
// to one schedule interval and the report says HasBaseline=false. It does NOT
// invent a previous score of zero, which would render a spurious +N on the
// first digest anyone ever receives — the failure T-2807 AC2 names.
func (s *Service) build(ctx context.Context, now time.Time, sched Schedule, prev *Run) docexport.DigestReport {
	periodEnd := now.Unix()
	periodStart := periodEnd - int64(sched.Every/time.Second)
	report := docexport.DigestReport{
		PeriodEnd:   periodEnd,
		GeneratedAt: periodEnd,
	}
	if prev != nil {
		periodStart = prev.PeriodEnd
		report.HasBaseline = true
		report.BaselineAt = prev.PeriodEnd
	}
	report.PeriodStart = periodStart

	report.Posture = s.gatherPosture(ctx, prev)
	live := s.gatherLive(&report)
	s.gatherTransitions(ctx, &report, live, periodStart, periodEnd)
	return report
}

// gatherPosture reads the current score and pairs it with the score the
// previous digest carried. Both halves are independently optional, and both
// are reported as such.
func (s *Service) gatherPosture(ctx context.Context, prev *Run) docexport.DigestPosture {
	var out docexport.DigestPosture
	if s.posture != nil {
		p, ok, err := s.posture.Latest(ctx)
		switch {
		case err != nil:
			// A posture read that failed is "no score", never a zero. Logged
			// so it is visible, and the digest still goes out: the rest of
			// the period is still worth reporting.
			s.log.Warn("digest: reading the posture score", "error", err)
		case ok:
			out.Scored = true
			out.Overall = p.Overall
			out.Qualified = p.Qualified
			out.Factors = p.Factors
		}
	}
	if prev != nil && prev.PostureOverall != PostureNotScored {
		out.PreviousScored = true
		out.Previous = int(prev.PostureOverall)
	}
	return out
}

// gatherLive splits the live findings stream into the two sections that
// describe the CURRENT state (capacity projections and unresolved drift) and
// returns the whole stream keyed by id, for the transition rows to describe
// themselves from.
func (s *Service) gatherLive(report *docexport.DigestReport) map[string]findings.Finding {
	live := map[string]findings.Finding{}
	if s.findings == nil {
		return live
	}
	for _, f := range s.findings.Findings() {
		live[f.ID] = f
		switch f.Source {
		case findings.SourceCapacity:
			report.Capacity = append(report.Capacity, itemFrom(f))
		case findings.SourceDrift:
			report.Drift = append(report.Drift, itemFrom(f))
		}
	}
	return live
}

// gatherTransitions fills the opened/closed sections from the retained
// transition history over [from, to].
//
// A finding may transition more than once in a window (new, then resolved,
// then new again). Each id appears at most once per section, because a digest
// reporting "vmbr0 carrier lost" three times says less than one that reports
// it once, not more.
func (s *Service) gatherTransitions(ctx context.Context, report *docexport.DigestReport, live map[string]findings.Finding, from, to int64) {
	if s.history == nil {
		return
	}
	transitions, err := s.history.Transitions(ctx, from, to)
	if err != nil {
		// The period's transitions are unavailable; the rest of the digest is
		// not. Reported rather than swallowed, and deliberately not fatal.
		s.log.Warn("digest: reading finding transitions for the period",
			"from", from, "to", to, "error", err)
		return
	}

	openedSeen, closedSeen := map[string]bool{}, map[string]bool{}
	for _, t := range transitions {
		switch t.Transition {
		case string(findings.TransitionNew), string(findings.TransitionEscalated):
			if !openedSeen[t.FindingID] {
				openedSeen[t.FindingID] = true
				report.Opened = append(report.Opened, itemForID(t.FindingID, live))
			}
		case string(findings.TransitionResolved):
			if !closedSeen[t.FindingID] {
				closedSeen[t.FindingID] = true
				report.Closed = append(report.Closed, itemForID(t.FindingID, live))
			}
		}
	}
}

func itemFrom(f findings.Finding) docexport.DigestItem {
	return docexport.DigestItem{
		ID:       f.ID,
		Check:    f.Check,
		Severity: f.Severity,
		Detail:   f.Detail,
		Nodes:    f.Nodes,
	}
}

// resolvedDetail is what a row says about a finding that is no longer in the
// stream. It is a statement of fact, not a placeholder: "resolved" is exactly
// why the description is gone.
const resolvedDetail = "no longer in the findings stream"

// itemForID describes a transitioned finding. One that is still live is
// described in full from the stream; one that has since resolved is not in the
// stream any more — that is what resolved means — so its row carries its id and
// says so rather than inventing a description for it.
func itemForID(id string, live map[string]findings.Finding) docexport.DigestItem {
	if f, ok := live[id]; ok {
		return itemFrom(f)
	}
	return docexport.DigestItem{ID: id, Check: checkFromID(id), Detail: resolvedDetail}
}

// checkFromID recovers the check name from a finding id, best-effort.
//
// Finding ids are "source:check|key" for this codebase's own health checks and
// "source:producer-key" for the adapted producers (internal/findings/types.go's
// Finding doc comment). The first segment is the source and the second, up to
// the key separator, is the check for every producer that follows the health
// scheme. An id that does not follow it yields the id itself, which is still
// more useful to a reader than an empty cell — and is never wrong, only less
// specific.
func checkFromID(id string) string {
	_, rest, ok := strings.Cut(id, ":")
	if !ok || rest == "" {
		return id
	}
	check, _, ok := strings.Cut(rest, "|")
	if !ok || check == "" {
		return rest
	}
	return check
}

// runFrom projects a rendered report onto the durable baseline row the next
// digest reads.
func runFrom(r docexport.DigestReport, status, detail string) Run {
	postureOverall := int64(PostureNotScored)
	if r.Posture.Scored {
		postureOverall = int64(r.Posture.Overall)
	}
	return Run{
		PeriodStart:    r.PeriodStart,
		PeriodEnd:      r.PeriodEnd,
		GeneratedAt:    r.GeneratedAt,
		PostureOverall: postureOverall,
		Opened:         int64(len(r.Opened)),
		Closed:         int64(len(r.Closed)),
		Drift:          int64(len(r.Drift)),
		Capacity:       int64(len(r.Capacity)),
		Quiet:          r.Quiet(),
		Status:         status,
		Detail:         detail,
	}
}
