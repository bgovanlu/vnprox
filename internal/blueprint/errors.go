// SPDX-License-Identifier: Apache-2.0

package blueprint

import "errors"

// Sentinel errors, per docs/development.md's Go standards ("sentinel
// errors in each package's errors.go"). Every returned error wraps one of
// these with fmt.Errorf("%w: ...") context so callers (internal/api) can
// errors.Is-branch into the right HTTP status without string matching.
var (
	// ErrNotFound is returned by Service.Get/Instantiate when no blueprint
	// (saved or starter) matches the requested id.
	ErrNotFound = errors.New("blueprint: not found")
	// ErrReadOnly is returned when a save/delete request targets a
	// bundled starter (docs/features/blueprints.md §1: "Ship with
	// starters (bundled, read-only, copy-to-edit)").
	ErrReadOnly = errors.New("blueprint: read-only")
	// ErrInvalid wraps every schema/structural validation failure
	// (Validate).
	ErrInvalid = errors.New("blueprint: invalid")
	// ErrInvalidParams wraps every param-value validation failure
	// (missing required param, bad CIDR/VID, unknown param name) —
	// T-603 AC4.
	ErrInvalidParams = errors.New("blueprint: invalid params")
)
