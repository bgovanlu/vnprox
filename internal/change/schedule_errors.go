package change

import "fmt"

// ErrInvalidScheduleWindow is returned by Schedule when windowStart is not
// strictly before windowEnd (docs/api.md: "422 ... bad window").
type ErrInvalidScheduleWindow struct {
	WindowStart int64
	WindowEnd   int64
}

func (e *ErrInvalidScheduleWindow) Error() string {
	return fmt.Sprintf("change: schedule window [%d, %d) is invalid: windowStart must be before windowEnd", e.WindowStart, e.WindowEnd)
}

// ErrInvalidMissedWindowPolicy is returned by Schedule when
// missedWindowPolicy is set to anything other than "skip" or
// "applyImmediately".
type ErrInvalidMissedWindowPolicy struct {
	Policy string
}

func (e *ErrInvalidMissedWindowPolicy) Error() string {
	return fmt.Sprintf("change: invalid missedWindowPolicy %q (want \"skip\" or \"applyImmediately\")", e.Policy)
}

// ErrMgmtPathUnattendedForbidden is returned by Schedule when the
// changeset's ops touch a node's resolved management path. This is an
// unconditional, server-side-only gate: there is no config flag or API
// parameter anywhere in this package, internal/api, or vnprox.toml that
// overrides it (docs/security.md's "no override in UI" stance on the T-203
// safety interlocks extends here to "no override at all" — even
// AllowDangerousOps, which can downgrade a T-203 safety-class *validation*
// finding from error to warning for an interactive apply, has no effect on
// this check). The API layer maps it to 422
// mgmt_path_unattended_forbidden (docs/api.md).
type ErrMgmtPathUnattendedForbidden struct {
	ChangesetID string
}

func (e *ErrMgmtPathUnattendedForbidden) Error() string {
	return fmt.Sprintf("change: changeset %s touches a management path and cannot be scheduled for unattended apply", e.ChangesetID)
}

// ErrScheduleAlreadyExists is returned by Schedule when changesetID already
// has a pending (not yet fired/missed/cancelled) schedule — call
// CancelSchedule first, or wait for it to resolve.
type ErrScheduleAlreadyExists struct {
	ChangesetID string
}

func (e *ErrScheduleAlreadyExists) Error() string {
	return fmt.Sprintf("change: changeset %s already has a pending schedule", e.ChangesetID)
}

// ErrInvalidCallbackToken is returned by AckSchedule when the presented
// token does not match the schedule's stored callback_token_hash.
type ErrInvalidCallbackToken struct {
	ChangesetID string
}

func (e *ErrInvalidCallbackToken) Error() string {
	return fmt.Sprintf("change: invalid callback token for changeset %s", e.ChangesetID)
}
