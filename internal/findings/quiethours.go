// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QuietHours is a daily local-wall-clock window during which an alert rule's
// deliveries are deferred (T-2407).
//
// Start and End are "HH:MM" in Zone. Start > End means the window crosses
// midnight — 22:00-06:00 — which is the *common* case for a quiet-hours
// feature, not an edge case, and is why every method here is written around
// the wrap rather than patching it in afterwards.
//
// Zone is an IANA name ("Europe/Bucharest"). Empty means the daemon's local
// zone. It is deliberately per rule: quiet hours is a statement about when a
// particular human is asleep, and a federated deployment has humans in more
// than one place.
type QuietHours struct {
	Start string
	End   string
	Zone  string
}

// Zero reports whether no quiet-hours window is configured.
func (q QuietHours) Zero() bool { return q.Start == "" && q.End == "" }

// Validate reports why the window is unusable, or nil.
func (q QuietHours) Validate() error {
	if q.Zero() {
		return nil
	}
	if q.Start == "" || q.End == "" {
		return fmt.Errorf("findings: quiet hours needs both a start and an end (got %q-%q)", q.Start, q.End)
	}
	if _, err := parseClock(q.Start); err != nil {
		return fmt.Errorf("findings: quiet hours start: %w", err)
	}
	if _, err := parseClock(q.End); err != nil {
		return fmt.Errorf("findings: quiet hours end: %w", err)
	}
	if q.Start == q.End {
		// Not a 24-hour window and not an empty one — it is unreadable, and
		// guessing which the operator meant is how alerts go missing for a
		// day. Refuse it.
		return fmt.Errorf("findings: quiet hours start and end are both %q; a zero-length window is ambiguous", q.Start)
	}
	if _, err := q.Location(); err != nil {
		return err
	}
	return nil
}

// Location resolves Zone.
func (q QuietHours) Location() (*time.Location, error) {
	if q.Zone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(q.Zone)
	if err != nil {
		return nil, fmt.Errorf("findings: quiet hours zone %q: %w", q.Zone, err)
	}
	return loc, nil
}

// Contains reports whether t falls inside the window, judged by local wall
// clock in Zone.
//
// Wall clock is the right comparison and not an approximation of one: an
// operator who writes 22:00-06:00 means "while I am asleep", which tracks the
// clock on the wall through a DST change, not a fixed number of hours from
// some epoch.
func (q QuietHours) Contains(t time.Time) (bool, error) {
	if q.Zero() {
		return false, nil
	}
	loc, err := q.Location()
	if err != nil {
		return false, err
	}
	start, err := parseClock(q.Start)
	if err != nil {
		return false, err
	}
	end, err := parseClock(q.End)
	if err != nil {
		return false, err
	}

	local := t.In(loc)
	now := local.Hour()*60 + local.Minute()

	if start < end {
		// An ordinary same-day window: 09:00-17:00.
		return now >= start && now < end, nil
	}
	// Crosses midnight: inside if we are at or past the start, or before the
	// end on the following morning.
	return now >= start || now < end, nil
}

// NextEnd returns the instant at which the window containing t ends. It is
// only meaningful when Contains(t) is true; for a t outside the window it
// still returns the next occurrence of the end time, which is harmless but
// not what a caller wants.
//
// DST is handled by construction rather than by special cases: the end
// instant is built with time.Date in the window's own zone, which resolves a
// wall-clock time to the correct offset on that date. On a spring-forward day
// a 02:30 end that does not exist normalises forward to 03:30 — an hour late,
// never skipped. On a fall-back day the first of the two 01:30s is chosen —
// possibly an hour early, never doubled. Both are stated here because both
// are choices, and the alternative to choosing is a bug on two days a year.
func (q QuietHours) NextEnd(t time.Time) (time.Time, error) {
	if q.Zero() {
		return t, nil
	}
	loc, err := q.Location()
	if err != nil {
		return time.Time{}, err
	}
	end, err := parseClock(q.End)
	if err != nil {
		return time.Time{}, err
	}

	local := t.In(loc)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), end/60, end%60, 0, 0, loc)
	if !candidate.After(local) {
		// Already past today's end time, so the window we are in ends
		// tomorrow. AddDate on the date parts (not +24h) keeps this correct
		// across a DST boundary, where "tomorrow at 06:00" is 23 or 25 hours
		// away rather than 24.
		candidate = time.Date(local.Year(), local.Month(), local.Day()+1, end/60, end%60, 0, 0, loc)
	}
	return candidate, nil
}

// parseClock parses "HH:MM" into minutes past midnight.
func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	hour, err := strconv.Atoi(h)
	if err != nil || len(h) != 2 || hour < 0 || hour > 23 {
		return 0, fmt.Errorf("%q has an out-of-range or malformed hour", s)
	}
	minute, err := strconv.Atoi(m)
	if err != nil || len(m) != 2 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%q has an out-of-range or malformed minute", s)
	}
	return hour*60 + minute, nil
}
