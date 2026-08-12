package e2egate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ExpiryLayout is the date format quarantine entries are written in. Date, not
// timestamp: a quarantine's granularity is "which day does this stop being
// tolerated", and a timestamp invites a timezone argument nobody wants to have
// while a build is red.
const ExpiryLayout = "2006-01-02"

// MaxQuarantineDays caps how far in the future an expiry may be set.
//
// Without a cap, "expires" is a formality — a quarantine to 2099 is a
// permanently disabled test wearing a deadline. Six weeks is two ordinary
// working sprints: long enough that a genuinely hard flake is not re-triaged
// every Monday, short enough that nobody forgets why the entry is there.
const MaxQuarantineDays = 42

// MinReasonLength is the shortest reason this package will accept. "flaky" is
// not a reason; it is a restatement of the fact that there is an entry.
const MinReasonLength = 20

// QuarantineEntry tolerates one test's failure until a stated date.
type QuarantineEntry struct {
	// File is the spec path exactly as the report names it ("e2e/scale.spec.ts").
	File string `json:"file"`
	// Title is the test's full title (describe ancestry joined to the test
	// title by TitleSeparator). Matching is exact: a prefix or substring match
	// would let one entry quietly cover tests nobody meant to quarantine.
	Title string `json:"title"`
	// Reason says what is known about the flake. Required, and length-checked.
	Reason string `json:"reason"`
	// Ticket is the card tracking the fix. Required: a quarantine with nobody
	// to un-quarantine it is a deletion with extra steps.
	Ticket string `json:"ticket"`
	// Expires is the last day the failure is tolerated, inclusive, in
	// ExpiryLayout. On the day after, the build fails whether or not the test
	// did.
	Expires string `json:"expires"`
}

// Quarantine is the parsed quarantine file.
type Quarantine struct {
	// Comment carries the file's own explanation of itself. Read and ignored;
	// it exists so the JSON can document itself for the next reader.
	Comment string            `json:"comment"`
	Entries []QuarantineEntry `json:"entries"`
}

// Key matches Outcome.Key.
func (e QuarantineEntry) Key() string { return e.File + TitleSeparator + e.Title }

// ExpiryDate parses the entry's expiry.
func (e QuarantineEntry) ExpiryDate() (time.Time, error) {
	t, err := time.Parse(ExpiryLayout, e.Expires)
	if err != nil {
		return time.Time{}, fmt.Errorf("quarantine %s: expiry %q is not a %s date: %w", e.Key(), e.Expires, ExpiryLayout, err)
	}
	return t, nil
}

// LoadQuarantine reads the quarantine file. A missing file is an empty
// quarantine, not an error: the healthy steady state of this repository is that
// there is nothing quarantined.
func LoadQuarantine(path string) (Quarantine, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied tooling input.
	if err != nil {
		if os.IsNotExist(err) {
			return Quarantine{}, nil
		}
		return Quarantine{}, fmt.Errorf("reading quarantine %s: %w", path, err)
	}
	var q Quarantine
	if err := json.Unmarshal(raw, &q); err != nil {
		return Quarantine{}, fmt.Errorf("parsing quarantine %s: %w", path, err)
	}
	return q, nil
}

// Problem is a quarantine entry the gate refuses to honour, and why.
type Problem struct {
	Entry  QuarantineEntry
	Reason string
}

// Validate checks every entry's shape against the rules above, independently of
// any test run. An entry that fails validation is NOT honoured — a malformed
// quarantine must not silently tolerate a failure.
func Validate(q Quarantine, now time.Time) []Problem {
	var problems []Problem
	seen := make(map[string]struct{}, len(q.Entries))
	for _, e := range q.Entries {
		switch {
		case strings.TrimSpace(e.File) == "":
			problems = append(problems, Problem{Entry: e, Reason: "no file"})
			continue
		case strings.TrimSpace(e.Title) == "":
			problems = append(problems, Problem{Entry: e, Reason: "no title"})
			continue
		}
		if _, dup := seen[e.Key()]; dup {
			problems = append(problems, Problem{Entry: e, Reason: "duplicate entry for the same test"})
			continue
		}
		seen[e.Key()] = struct{}{}

		if len(strings.TrimSpace(e.Reason)) < MinReasonLength {
			problems = append(problems, Problem{
				Entry:  e,
				Reason: fmt.Sprintf("reason is shorter than %d characters; say what is known about the flake", MinReasonLength),
			})
		}
		if strings.TrimSpace(e.Ticket) == "" {
			problems = append(problems, Problem{Entry: e, Reason: "no ticket; a quarantine nobody owns never ends"})
		}
		expiry, err := e.ExpiryDate()
		if err != nil {
			problems = append(problems, Problem{Entry: e, Reason: err.Error()})
			continue
		}
		if expiry.After(day(now).AddDate(0, 0, MaxQuarantineDays)) {
			problems = append(problems, Problem{
				Entry:  e,
				Reason: fmt.Sprintf("expires %s, more than %d days out; that is a disabled test, not a quarantine", e.Expires, MaxQuarantineDays),
			})
		}
	}
	return problems
}

// day truncates to the entry's own granularity (UTC calendar date), so "expires
// today" tolerates the whole of today regardless of what hour the build runs.
func day(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// Expired reports whether the entry's expiry is strictly before now's calendar
// date — i.e. the entry is tolerated through the whole of its expiry day.
func (e QuarantineEntry) Expired(now time.Time) (bool, error) {
	expiry, err := e.ExpiryDate()
	if err != nil {
		return false, err
	}
	return day(now).After(expiry), nil
}
