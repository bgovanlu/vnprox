// SPDX-License-Identifier: Apache-2.0

package pvemock

import "errors"

// Sentinel errors returned by fixture loading, validation, and the mock
// server's request handlers. Callers should use errors.Is/errors.As rather
// than comparing formatted messages.
var (
	// ErrFixtureInvalid indicates a fixture failed referential-integrity
	// validation at load time (e.g. a bridge port names a NIC that does
	// not exist on the node). Fixtures must never load "successfully"
	// into a broken in-memory state.
	ErrFixtureInvalid = errors.New("pvemock: fixture invalid")

	// ErrNotFound indicates a requested node/iface/guest/zone/etc. does
	// not exist in the loaded fixture/state.
	ErrNotFound = errors.New("pvemock: not found")

	// ErrAuthFailed indicates a login attempt failed (bad user/password).
	ErrAuthFailed = errors.New("pvemock: authentication failed")

	// ErrForbidden indicates the authenticated session lacks the PVE
	// privilege required for the requested operation.
	ErrForbidden = errors.New("pvemock: forbidden")

	// ErrTaskFailed is stored on a task when failure injection (fixture
	// default or per-request override) triggers a simulated apply
	// failure.
	ErrTaskFailed = errors.New("pvemock: task failed (injected)")

	// ErrFRRUnavailable indicates a node's fixture declares no `frr:`
	// block at all (T-404): the mock's modeled equivalent of a real node
	// that never installed/ran FRR — internal/host's FixtureReader maps
	// this onto its own ErrFRRUnavailable sentinel (see that package's
	// fixture.go doc comment).
	ErrFRRUnavailable = errors.New("pvemock: frr not configured")

	// ErrCorosyncUnavailable indicates a node's fixture declares no
	// `corosync:` block at all (T-803): the mock's modeled equivalent of a
	// real node running no corosync at all (e.g. a single, not-yet-
	// clustered node) — internal/host's FixtureReader maps this onto its
	// own ErrCorosyncUnavailable sentinel, mirroring ErrFRRUnavailable's
	// exact convention.
	ErrCorosyncUnavailable = errors.New("pvemock: corosync not configured")
)
