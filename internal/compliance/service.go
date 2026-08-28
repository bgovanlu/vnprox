// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// service.go assembles a Report from the daemon's live services. Every seam
// is an interface taking or returning this package's own projections, so
// internal/compliance imports neither the findings engine nor the change
// engine — the same decoupling internal/posture uses, for the same reason:
// the evaluator must be drivable from a test table.
//
// Nothing here writes. There is no path from this file into the change
// engine, the store's mutable tables, or a node.

// Transition is one retained finding-transition record (store.FindingEvent).
// It carries the finding id and what happened, which is everything the
// history retains — deliberately NOT the severity, which is why a
// historical report grades every open finding as failing.
type Transition struct {
	FindingID  string
	Transition string
	At         int64
}

// Transition kinds, mirroring internal/findings' notifier vocabulary.
const (
	TransitionNew       = "new"
	TransitionEscalated = "escalated"
	TransitionResolved  = "resolved"
)

// FindingsSource supplies the currently-open findings for a live report.
type FindingsSource interface {
	ComplianceFindings(ctx context.Context) ([]FindingRef, error)
}

// HistorySource supplies the retained finding-transition history for an
// as-of report, and how far back it reaches.
type HistorySource interface {
	// Transitions returns every retained transition with At <= until,
	// ascending by At.
	Transitions(ctx context.Context, until time.Time) ([]Transition, error)
	// Earliest returns the earliest retained transition instant. ok is
	// false when nothing is retained.
	Earliest(ctx context.Context) (t time.Time, ok bool, err error)
}

// PostureSource is T-1607's read model. It is exactly api.PostureService's
// shape, so the daemon's existing posture read adapter satisfies it without
// a second path to posture_scores.
type PostureSource interface {
	Latest(ctx context.Context) (posture.Posture, bool, error)
	History(ctx context.Context, limit int) ([]posture.Posture, error)
}

// PolicySource is T-2601's installed rule set plus per-rule bookkeeping,
// projected. The composition root adapts change.Service.PolicyStatus onto
// it.
type PolicySource interface {
	CompliancePolicy(ctx context.Context) (PolicyState, error)
}

// postureHistoryLimit bounds how far back the as-of posture lookup pages.
// The posture job runs daily and posture_scores is retention-bounded, so
// 400 rows is past the table's own ceiling.
const postureHistoryLimit = 400

// Service answers compliance report requests. Every dependency is optional
// (nil-safe): a daemon with no policy store, no posture job or no retained
// history still produces a report, with the missing surface reported as not
// evaluated rather than silently satisfied.
type Service struct {
	Findings FindingsSource
	History  HistorySource
	Posture  PostureSource
	Policy   PolicySource
	Now      func() time.Time
	Log      *slog.Logger
	// ProductVersion is the vnprox build stamped on every report.
	ProductVersion string
	// CheckUniverse describes where KnownChecks came from.
	CheckUniverse string
	Profiles      []Profile
	// KnownChecks is the check universe the unmapped-check list is
	// computed against (findings.AllCheckNames() in production).
	KnownChecks []string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ProfileSummary is one installed profile as GET /compliance lists it.
type ProfileSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Version       string `json:"version"`
	Description   string `json:"description,omitempty"`
	Notice        string `json:"notice"`
	ControlCount  int    `json:"controlCount"`
	MappedChecks  int    `json:"mappedChecks"`
	UnmappedCount int    `json:"unmappedControls"`
}

// ListProfiles summarizes every installed profile.
func (s *Service) ListProfiles() []ProfileSummary {
	out := make([]ProfileSummary, 0, len(s.Profiles))
	for _, p := range s.Profiles {
		unmapped := 0
		for _, c := range p.Controls {
			if len(c.Evidence) == 0 {
				unmapped++
			}
		}
		out = append(out, ProfileSummary{
			ID: p.ID, Title: p.Title, Version: p.Version, Description: p.Description,
			Notice: p.Notice, ControlCount: len(p.Controls),
			MappedChecks: len(p.MappedChecks()), UnmappedCount: unmapped,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Service) profile(id string) (Profile, error) {
	available := make([]string, 0, len(s.Profiles))
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, nil
		}
		available = append(available, p.ID)
	}
	sort.Strings(available)
	return Profile{}, &ErrUnknownProfile{ID: id, Available: available}
}

// Report builds the report for profileID. A zero asOf produces a live
// report; a non-zero asOf produces one reconstructed from retained history,
// or refuses with *ErrOutsideRetention naming the earliest date it could
// have covered.
func (s *Service) Report(ctx context.Context, profileID string, asOf time.Time) (Report, error) {
	p, err := s.profile(profileID)
	if err != nil {
		return Report{}, err
	}

	in := Inputs{
		Now:            s.now(),
		ProductVersion: s.ProductVersion,
		KnownChecks:    s.KnownChecks,
		CheckUniverse:  s.CheckUniverse,
	}

	if asOf.IsZero() {
		if in.Findings, err = s.liveFindings(ctx); err != nil {
			return Report{}, err
		}
		in.Posture, in.PostureOK = s.postureLatest(ctx)
		in.Policy = s.policyState(ctx)
	} else {
		if asOf.After(in.Now) {
			return Report{}, &ErrFutureAsOf{Requested: asOf, Now: in.Now}
		}
		if in.Findings, err = s.historicalFindings(ctx, asOf); err != nil {
			return Report{}, err
		}
		in.AsOf = asOf
		in.Posture, in.PostureOK = s.postureAt(ctx, asOf)
	}

	return Evaluate(p, in), nil
}

func (s *Service) liveFindings(ctx context.Context) ([]FindingRef, error) {
	if s.Findings == nil {
		return nil, nil
	}
	refs, err := s.Findings.ComplianceFindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("compliance: reading the findings stream: %w", err)
	}
	return refs, nil
}

// historicalFindings reconstructs the findings open at asOf, refusing when
// asOf predates the retained history.
func (s *Service) historicalFindings(ctx context.Context, asOf time.Time) ([]FindingRef, error) {
	if s.History == nil {
		return nil, &ErrOutsideRetention{Requested: asOf}
	}
	earliest, ok, err := s.History.Earliest(ctx)
	if err != nil {
		return nil, fmt.Errorf("compliance: reading the retained finding history's earliest record: %w", err)
	}
	if !ok {
		return nil, &ErrOutsideRetention{Requested: asOf}
	}
	if asOf.Before(earliest) {
		return nil, &ErrOutsideRetention{Requested: asOf, Earliest: earliest, HasEarliest: true}
	}
	events, err := s.History.Transitions(ctx, asOf)
	if err != nil {
		return nil, fmt.Errorf("compliance: reading the retained finding history: %w", err)
	}
	return ReplayOpen(events), nil
}

// ReplayOpen reconstructs which findings were open at the end of events,
// which must be ascending by At and already bounded to the target instant.
//
// A finding is open if its LAST transition is `new` or `escalated`. Severity
// is not retained by finding_events, so every reconstructed finding carries
// an empty Severity — which the evaluator grades as meeting every threshold.
// A historical report is therefore stricter than the live one was, never
// more lenient, and it says so.
//
// The check name is recovered from the finding id, whose "<source>:<check>|<key>"
// shape every producer stamps (internal/findings' newHealthFinding and the
// drift/lldp adapters alike). An id that does not carry a parseable check
// yields an empty check name, which maps to no control's evidence and is
// therefore reported nowhere rather than misattributed.
func ReplayOpen(events []Transition) []FindingRef {
	open := map[string]bool{}
	for _, e := range events {
		switch e.Transition {
		case TransitionResolved:
			open[e.FindingID] = false
		case TransitionNew, TransitionEscalated:
			open[e.FindingID] = true
		default:
			// An unknown transition kind must not silently clear a
			// finding: leave whatever the last known state was.
		}
	}
	ids := make([]string, 0, len(open))
	for id, isOpen := range open {
		if isOpen {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]FindingRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, FindingRef{ID: id, Check: CheckFromFindingID(id)})
	}
	return out
}

// CheckFromFindingID recovers the check name from a unified finding id
// ("<source>:<check>|<key>"). It returns "" when the id does not carry one.
func CheckFromFindingID(id string) string {
	_, rest, ok := strings.Cut(id, ":")
	if !ok {
		return ""
	}
	check, _, _ := strings.Cut(rest, "|")
	return strings.TrimSpace(check)
}

func (s *Service) postureLatest(ctx context.Context) (posture.Posture, bool) {
	if s.Posture == nil {
		return posture.Posture{}, false
	}
	p, ok, err := s.Posture.Latest(ctx)
	if err != nil {
		s.log().Warn("compliance: reading the latest posture score", "error", err)
		return posture.Posture{}, false
	}
	return p, ok
}

// postureAt returns the newest posture score computed at or before asOf.
// There is no interpolation: a score is a measurement, and inventing one for
// a date nothing was computed would be the same lie as a partial report.
func (s *Service) postureAt(ctx context.Context, asOf time.Time) (posture.Posture, bool) {
	if s.Posture == nil {
		return posture.Posture{}, false
	}
	history, err := s.Posture.History(ctx, postureHistoryLimit)
	if err != nil {
		s.log().Warn("compliance: reading posture history", "error", err)
		return posture.Posture{}, false
	}
	var best posture.Posture
	found := false
	for _, p := range history {
		if p.ComputedAt > asOf.Unix() {
			continue
		}
		if !found || p.ComputedAt > best.ComputedAt {
			best, found = p, true
		}
	}
	return best, found
}

func (s *Service) policyState(ctx context.Context) PolicyState {
	if s.Policy == nil {
		return PolicyState{}
	}
	st, err := s.Policy.CompliancePolicy(ctx)
	if err != nil {
		s.log().Warn("compliance: reading the installed policy set", "error", err)
		return PolicyState{}
	}
	return st
}
