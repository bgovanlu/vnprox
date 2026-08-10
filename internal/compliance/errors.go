package compliance

import (
	"fmt"
	"time"
)

// ErrUnknownProfile is returned when no installed profile has the requested
// id. It names the ids that do exist, because "not found" without them
// sends the caller to the source.
type ErrUnknownProfile struct {
	ID        string
	Available []string
}

func (e *ErrUnknownProfile) Error() string {
	return fmt.Sprintf("compliance: no profile %q is installed (installed: %v)", e.ID, e.Available)
}

// ErrOutsideRetention is returned when a report is requested for a date the
// retained evidence does not reach back to.
//
// WHY THIS IS AN ERROR AND NOT A THINNER REPORT. A compliance report
// assembled from evidence that does not exist reads as "these controls were
// in this state on that date". They were not — nothing was observed. This is
// the same failure mode T-2704 names for an empty topology diff: absence
// rendered as a finding of no change is a false statement, and the only
// honest response is to refuse and say how far back the evidence does go.
type ErrOutsideRetention struct {
	// Requested is the date asked for.
	Requested time.Time
	// Earliest is the earliest instant retained evidence covers. Zero when
	// NO evidence is retained at all (HasEarliest false).
	Earliest    time.Time
	HasEarliest bool
}

func (e *ErrOutsideRetention) Error() string {
	if !e.HasEarliest {
		return fmt.Sprintf(
			"compliance: cannot report as of %s: no finding history is retained on this daemon at all, so no past date can be reported",
			e.Requested.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf(
		"compliance: cannot report as of %s: the retained finding history begins at %s; no report is produced for a date the evidence does not cover",
		e.Requested.UTC().Format(time.RFC3339), e.Earliest.UTC().Format(time.RFC3339))
}

// ErrFutureAsOf is returned when a report is requested for a date after now.
// A report about the future is not a partial report either.
type ErrFutureAsOf struct {
	Requested time.Time
	Now       time.Time
}

func (e *ErrFutureAsOf) Error() string {
	return fmt.Sprintf("compliance: cannot report as of %s: that is after this daemon's current time (%s)",
		e.Requested.UTC().Format(time.RFC3339), e.Now.UTC().Format(time.RFC3339))
}
