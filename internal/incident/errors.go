// SPDX-License-Identifier: Apache-2.0

package incident

import "errors"

// Sentinel errors. internal/api maps each to docs/api.md's error envelope;
// nothing here ever reaches a client as prose alone.
var (
	// ErrTitleRequired: an incident with no title is unfindable a week later,
	// which is when it is read.
	ErrTitleRequired = errors.New("incident: a title is required")

	// ErrWindowInverted: endedAt precedes startedAt. Refused rather than
	// silently swapped — the change engine refuses an inverted diff range for
	// the same reason (*change.ErrDiffRangeInverted).
	ErrWindowInverted = errors.New("incident: the window ends before it starts")

	// ErrAnnotationEmpty: an annotation with no text records nothing.
	ErrAnnotationEmpty = errors.New("incident: an annotation needs a body")

	// ErrAlreadyClosed / ErrAlreadyOpen: the lifecycle refuses a no-op rather
	// than pretending it happened, so a UI that double-submits learns about
	// it instead of reporting a second close that never occurred.
	ErrAlreadyClosed = errors.New("incident: this incident is already closed")
	ErrAlreadyOpen   = errors.New("incident: this incident is already open")

	// ErrExportUnavailable: an export was requested on a service built
	// without a bundler. The timeline is still readable; only the artifact is
	// not producible.
	ErrExportUnavailable = errors.New("incident: exporting is not configured on this node")
)
