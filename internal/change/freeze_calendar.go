// SPDX-License-Identifier: Apache-2.0

// freeze_calendar.go is a best-effort READER for the change-calendar view
// (T-4006): it does not evaluate anything and is never consulted by
// EvaluatePolicy. The one and only enforcement path stays policy_eval.go's
// EvaluatePolicy, exactly as the card requires ("both paths must see the
// same freeze-window data, from one source" — the source being the
// PolicySet itself, not this file's interpretation of it).
//
// A freeze window IS an ordinary PolicyRule (policy.go), so rendering one
// on a timeline means pattern-matching its Match conditions for the
// well-known time.* shapes (a weekday set, a minute-of-day range, an
// absolute epoch range, ...) that ExampplePolicySet-style rules actually
// use. A rule this cannot recognize is not hidden — it is still listed
// (id, description, severity) — it simply gets no drawable window, which is
// the honest answer for a `time.*` condition combination too irregular to
// summarize (the same "best-effort and says so" discipline T-2605's
// topology preview documents for its own unprojectable ops).

package change

import (
	"context"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/store"
)

// FreezeWindowView is the calendar's render-ready shape for one freeze rule
// (a PolicyRule tagged PolicyTagFreeze) — GET /calendar's `freezeWindows`
// entries.
//
// Field order is densest-pointer-first (bare pointers, then strings, then
// slices, then the trailing bool): govet's fieldalignment measures bytes up
// to the final pointer, so a pointer-free field sitting above one costs
// alignment for nothing.
type FreezeWindowView struct {
	// MinuteOfDayStart/End, both non-nil together, is the recurring daily
	// local-wall-clock sub-window — time.minuteOfDay gte/gt + lt/lte.
	MinuteOfDayStart *int `json:"minuteOfDayStart,omitempty"`
	MinuteOfDayEnd   *int `json:"minuteOfDayEnd,omitempty"`
	// EpochStart/End, both non-nil together, is a one-off absolute-instant
	// range — time.now gte/gt + lt/lte, unix seconds.
	EpochStart  *int64 `json:"epochStart,omitempty"`
	EpochEnd    *int64 `json:"epochEnd,omitempty"`
	RuleID      string `json:"ruleId"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	// Zone is the rule's own PolicyRule.Zone — "" when the rule uses only
	// the zone-free time.now.
	Zone string `json:"zone,omitempty"`
	// Weekdays, when non-empty, is the recurring weekday set ("fri") —
	// time.weekday eq/in.
	Weekdays []string `json:"weekdays,omitempty"`
	// DaysOfMonth/Months, when non-empty, are the recurring monthly
	// selector — time.dayOfMonth/time.month eq/in.
	DaysOfMonth []int `json:"daysOfMonth,omitempty"`
	Months      []int `json:"months,omitempty"`
	// Recognized is false when this rule's Match conditions used no
	// well-known time.* shape this extractor understands — every shape
	// field above (Weekdays, DaysOfMonth, Months, MinuteOfDayStart/End,
	// EpochStart/End) is then empty, and the frontend renders the rule by
	// description alone, with no drawable box.
	Recognized bool `json:"recognized"`
}

// ExtractFreezeWindows reads every PolicyTagFreeze-tagged rule in set and
// renders what it can of each one's Match conditions into a
// FreezeWindowView, in rule order. Pure and read-only: it never touches the
// store, never evaluates anything, and its output is never fed back into
// EvaluatePolicy.
func ExtractFreezeWindows(set PolicySet) []FreezeWindowView {
	var out []FreezeWindowView
	for _, rule := range set.Rules {
		if !hasTag(rule.Tags, PolicyTagFreeze) {
			continue
		}
		out = append(out, extractFreezeWindow(rule))
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

func extractFreezeWindow(rule PolicyRule) FreezeWindowView {
	v := FreezeWindowView{
		RuleID: rule.ID, Description: rule.Description, Severity: string(rule.Severity), Zone: rule.Zone,
	}
	for _, c := range rule.Match {
		switch c.Field {
		case policyFieldTimeWeekday:
			if ss, ok := stringLiterals(c); ok && (c.Op == PolicyOpEq || c.Op == PolicyOpIn) {
				v.Weekdays = append(v.Weekdays, ss...)
				v.Recognized = true
			}
		case policyFieldTimeDayOfMonth:
			if ns, ok := intLiterals(c); ok && (c.Op == PolicyOpEq || c.Op == PolicyOpIn) {
				v.DaysOfMonth = append(v.DaysOfMonth, ns...)
				v.Recognized = true
			}
		case policyFieldTimeMonth:
			if ns, ok := intLiterals(c); ok && (c.Op == PolicyOpEq || c.Op == PolicyOpIn) {
				v.Months = append(v.Months, ns...)
				v.Recognized = true
			}
		case policyFieldTimeMinuteOfDay:
			if n, ok := intLiteral(c.Value); ok {
				switch c.Op {
				case PolicyOpGte, PolicyOpGt:
					start := n
					v.MinuteOfDayStart = &start
					v.Recognized = true
				case PolicyOpLte, PolicyOpLt:
					end := n
					v.MinuteOfDayEnd = &end
					v.Recognized = true
				}
			}
		case policyFieldTimeNow:
			if n, ok := int64Literal(c.Value); ok {
				switch c.Op {
				case PolicyOpGte, PolicyOpGt:
					start := n
					v.EpochStart = &start
					v.Recognized = true
				case PolicyOpLte, PolicyOpLt:
					end := n
					v.EpochEnd = &end
					v.Recognized = true
				}
			}
		}
	}
	sort.Strings(v.Weekdays)
	sort.Ints(v.DaysOfMonth)
	sort.Ints(v.Months)
	return v
}

func stringLiterals(c PolicyCondition) ([]string, bool) {
	lits := conditionLiterals(c)
	out := make([]string, 0, len(lits))
	for _, v := range lits {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, len(out) > 0
}

func intLiterals(c PolicyCondition) ([]int, bool) {
	lits := conditionLiterals(c)
	out := make([]int, 0, len(lits))
	for _, v := range lits {
		n, ok := intLiteral(v)
		if !ok {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

func intLiteral(v any) (int, bool) {
	n, ok := policyNumber(v)
	if !ok {
		return 0, false
	}
	return int(n), true
}

func int64Literal(v any) (int64, bool) {
	n, ok := policyNumber(v)
	if !ok {
		return 0, false
	}
	return int64(n), true
}

// CalendarView is `GET /calendar`'s whole response: every declared freeze
// window (from the cluster's installed policy set) alongside every
// currently-pending scheduled changeset — the same two window models
// (policy.go's PolicyRule and schedule.go's Schedule) an operator needs on
// one timeline to see WHY an apply would be refused before staging it —
// plus, since T-4007, every declared node maintenance window
// (maintenance.go's MaintenanceWindow), rendered on the SAME timeline
// because a maintenance window is, in that card's own words, "another
// declared time range" alongside a freeze window and a schedule, not a
// fourth unrelated concept needing its own view.
type CalendarView struct {
	FreezeWindows      []FreezeWindowView      `json:"freezeWindows"`
	Schedules          []Schedule              `json:"schedules"`
	MaintenanceWindows []MaintenanceWindowView `json:"maintenanceWindows"`
}

// MaintenanceWindowView is one declared maintenance window as GET /calendar
// renders it: the stored MaintenanceWindow plus a computed Active flag —
// unlike a freeze window (an opaque PolicyRule this file has to pattern-
// match), a maintenance window is already first-class typed data, so no
// extraction step is needed; the only thing the calendar adds is "is this
// one live right now".
type MaintenanceWindowView struct {
	MaintenanceWindow
	Active bool `json:"active"`
}

// Calendar returns GET /calendar's response for the local cluster: every
// declared freeze window in the currently-installed policy set, plus every
// currently-pending scheduled changeset (store.ScheduleStatusPending —
// the same set TickSchedules itself scans, so "what's scheduled" here is
// never stale relative to what the scheduler will actually act on).
//
// A daemon with neither policies nor schedules configured (both optional,
// nil-safe stores) returns an empty-but-valid CalendarView rather than an
// error — the same "absent feature is a no-op" convention every other
// optional store in this Service follows.
func (s *Service) Calendar(ctx context.Context) (CalendarView, error) {
	set, _, err := s.storedPolicySet(ctx, s.localClusterID)
	if err != nil {
		return CalendarView{}, err
	}
	view := CalendarView{FreezeWindows: ExtractFreezeWindows(set)}

	if s.schedules != nil {
		rows, err := s.schedules.ListByStatus(ctx, store.ScheduleStatusPending)
		if err != nil {
			return CalendarView{}, fmt.Errorf("change: listing pending schedules for calendar: %w", err)
		}
		for _, row := range rows {
			view.Schedules = append(view.Schedules, scheduleFromRow(row))
		}
	}

	// T-4007: declared node maintenance windows, on the same timeline.
	if s.maintenanceWindows != nil {
		windows, err := s.MaintenanceWindows(ctx)
		if err != nil {
			return CalendarView{}, err
		}
		now := s.now()
		for _, w := range windows {
			view.MaintenanceWindows = append(view.MaintenanceWindows, MaintenanceWindowView{MaintenanceWindow: w, Active: w.Active(now)})
		}
	}
	return view, nil
}
