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
)
