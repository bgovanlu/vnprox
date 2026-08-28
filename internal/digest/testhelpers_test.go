// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/posture"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeStore is an in-memory digest.Store.
//
// The schedule is a MUTABLE field a test can change between ticks without
// rebuilding the Service — which is exactly what "a schedule change takes
// effect without a restart" has to mean if the criterion is to have any
// content.
//
//nolint:govet // fieldalignment: a test double; readability beats packing.
type fakeStore struct {
	mu       sync.Mutex
	sched    Schedule
	schedSet bool
	schedErr error
	runs     []Run
	recErr   error
	// schedReads counts Schedule calls, so a test can prove the schedule is
	// re-read rather than cached.
	schedReads int
}

func (s *fakeStore) Schedule(context.Context) (Schedule, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedReads++
	if s.schedErr != nil {
		return Schedule{}, false, s.schedErr
	}
	return s.sched, s.schedSet, nil
}

func (s *fakeStore) setSchedule(sched Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sched = sched
	s.schedSet = true
}

func (s *fakeStore) LatestRun(context.Context) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		return Run{}, false, nil
	}
	newest := s.runs[0]
	for _, r := range s.runs[1:] {
		if r.PeriodEnd >= newest.PeriodEnd {
			newest = r
		}
	}
	return newest, true, nil
}

func (s *fakeStore) RecordRun(_ context.Context, r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recErr != nil {
		return s.recErr
	}
	s.runs = append(s.runs, r)
	return nil
}

func (s *fakeStore) recorded() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Run(nil), s.runs...)
}

func (s *fakeStore) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schedReads
}

// stubPosture is a PostureSource returning a fixed score.
//
//nolint:govet // fieldalignment: a test double; readability beats packing.
type stubPosture struct {
	p      posture.Posture
	ok     bool
	err    error
	called int
}

func (s *stubPosture) Latest(context.Context) (posture.Posture, bool, error) {
	s.called++
	return s.p, s.ok, s.err
}

// stubFindings is a FindingsSource returning a fixed stream.
type stubFindings struct{ items []findings.Finding }

func (s stubFindings) Findings() []findings.Finding { return s.items }

// stubHistory is a HistorySource that RECORDS THE WINDOW it was asked for.
// That recording is the assertion behind "deltas are computed against the
// previous digest, not against an arbitrary window": the window a digest
// queries is observable, so a regression to "the last seven days" is visible
// rather than inferred.
type stubHistory struct {
	transitions []Transition
	err         error
	fromSeen    []int64
	toSeen      []int64
}

func (s *stubHistory) Transitions(_ context.Context, from, to int64) ([]Transition, error) {
	s.fromSeen = append(s.fromSeen, from)
	s.toSeen = append(s.toSeen, to)
	if s.err != nil {
		return nil, s.err
	}
	return s.transitions, nil
}

// captureNotifier records what was handed to the delivery path, for the tests
// that are about assembly rather than about delivery.
//
//nolint:govet // fieldalignment: a test double; the mutex sits with what it guards.
type captureNotifier struct {
	mu    sync.Mutex
	sent  []findings.Finding
	kinds []findings.TransitionKind
	err   error
}

func (n *captureNotifier) Notify(_ context.Context, f findings.Finding, kind findings.TransitionKind) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, f)
	n.kinds = append(n.kinds, kind)
	return n.err
}

func (n *captureNotifier) only() findings.Finding {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sent) != 1 {
		panic(fmt.Sprintf("captureNotifier: %d deliveries, want exactly 1", len(n.sent)))
	}
	return n.sent[0]
}

// memPending is an in-memory findings.PendingStore — the durable deferral
// queue T-2407's quiet hours hold events in. Used real (not stubbed out) so
// the quiet-hours legs exercise findings.Scheduler itself.
//
//nolint:govet // fieldalignment: a test double; the mutex sits with what it guards.
type memPending struct {
	mu   sync.Mutex
	next int
	rows []findings.PendingDelivery
}

func (m *memPending) AddPending(_ context.Context, p findings.PendingDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	p.ID = fmt.Sprintf("pend-%d", m.next)
	m.rows = append(m.rows, p)
	return nil
}

func (m *memPending) PendingFlushAt(_ context.Context, ruleID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var earliest time.Time
	found := false
	for _, r := range m.rows {
		if r.RuleID != ruleID {
			continue
		}
		if !found || r.FlushAt.Before(earliest) {
			earliest, found = r.FlushAt, true
		}
	}
	return earliest, found, nil
}

func (m *memPending) DuePending(_ context.Context, now time.Time) ([]findings.PendingDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []findings.PendingDelivery
	for _, r := range m.rows {
		if !r.FlushAt.After(now) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memPending) DeletePending(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := m.rows[:0]
	for _, r := range m.rows {
		if !drop[r.ID] {
			kept = append(kept, r)
		}
	}
	m.rows = kept
	return nil
}

func (m *memPending) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

// fixedRules is a findings.AlertRuleProvider over a static rule set.
type fixedRules struct {
	err   error
	rules []findings.AlertRule
}

func (f fixedRules) AlertRules(context.Context) ([]findings.AlertRule, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rules, nil
}

// recordingRecorder is a findings.DeliveryRecorder capturing the delivery log
// the Settings UI would show.
//
//nolint:govet // fieldalignment: a test double; the mutex sits with what it guards.
type recordingRecorder struct {
	mu   sync.Mutex
	rows []findings.AlertDelivery
}

func (r *recordingRecorder) RecordDelivery(_ context.Context, d findings.AlertDelivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, d)
	return nil
}

func (r *recordingRecorder) all() []findings.AlertDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]findings.AlertDelivery(nil), r.rows...)
}

// mustTime parses an RFC3339 instant in a test fixture.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// scoredPosture is a representative posture score with named factors.
func scoredPosture(overall int, at int64) posture.Posture {
	return posture.Posture{
		Overall:    overall,
		ComputedAt: at,
		Factors: []posture.Factor{
			{
				Name: posture.FactorSPOF, Detail: "1 single point of failure",
				Value: 1, ScorePct: 70, Weight: 30, Contribution: 21, Evaluated: true,
			},
			{
				Name: posture.FactorSegmentation, Detail: "all segments isolated",
				Value: 1, ScorePct: 100, Weight: 25, Contribution: 25, Evaluated: true,
			},
		},
	}
}
