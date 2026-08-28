// SPDX-License-Identifier: Apache-2.0

package runbook

import "errors"

var (
	// ErrRunbookNotFound is returned when a caller names a runbook that
	// does not exist in the catalog.
	ErrRunbookNotFound = errors.New("runbook: no such runbook")
	// ErrFindingNotFound is returned when a caller names a finding id the
	// FindingsProvider does not currently report.
	ErrFindingNotFound = errors.New("runbook: no such finding")
	// ErrNotAttached is returned when the named runbook's CheckName does
	// not match the target finding's Check — a runbook is only ever
	// offered for, and only ever runs against, its own attached check.
	ErrNotAttached = errors.New("runbook: this runbook is not attached to that finding's check")
	// ErrUnimplementedTemplate is returned when a Runbook names a
	// TemplateKind Render's switch does not (yet) implement — the closed-
	// vocabulary failure mode, never silently ignored.
	ErrUnimplementedTemplate = errors.New("runbook: template not implemented")
	// ErrNothingToDo is returned when a read-check finds the finding's
	// underlying condition has already resolved (the referenced object was
	// already deleted, recreated, or fixed by other means since the
	// finding fired) — mirroring T-4002's Ansible "matches live state ->
	// changed:false, stage nothing" idempotency discipline. Never wraps a
	// staged, empty, or invalid changeset: Prepare stages nothing at all
	// when Render returns this.
	ErrNothingToDo = errors.New("runbook: read-check found nothing left to remediate")
	// ErrUnsupportedRuleOrigin is returned by TemplateDeleteUnusedFwRule
	// for a cluster- or security-group-scoped rule: only guest-scoped
	// rules are supported today (see renderDeleteUnusedFwRule's doc
	// comment for why).
	ErrUnsupportedRuleOrigin = errors.New("runbook: firewall rule origin not supported by this runbook")
	// ErrMalformedFindingID is returned when a finding's ID does not match
	// the format its own check's producer is documented to emit — see
	// parseFwRuleUnusedFindingID.
	ErrMalformedFindingID = errors.New("runbook: finding id does not match the expected format for this check")
	// ErrRefNotFound is returned when a finding's own Refs name an entity
	// the current inventory snapshot no longer has any record of at all
	// (distinct from ErrNothingToDo's "no longer needs fixing": this is
	// "the runbook cannot tell what it would even be fixing").
	ErrRefNotFound = errors.New("runbook: finding's referenced entity was not found in the current snapshot")
)
