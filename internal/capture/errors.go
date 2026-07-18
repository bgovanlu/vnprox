package capture

import "errors"

var (
	// ErrInvalidFilter is returned when a submitted BPF filter is rejected
	// by the server-side validator (unsafe characters, too many primitives,
	// or over the length ceiling) — see bpf.go. The capture Agent is never
	// invoked in this case (T-1301 AC3).
	ErrInvalidFilter = errors.New("capture: invalid filter")

	// ErrUnresolvableTarget is returned when a target Ref cannot be scoped
	// to a concrete capture interface on its node — the "filter can't be
	// scoped to the target's own interface" rejection the card requires,
	// also raised before any capture process is invoked.
	ErrUnresolvableTarget = errors.New("capture: target cannot be scoped to a capture interface")

	// ErrNotFound is returned when a capture group / session id is unknown.
	ErrNotFound = errors.New("capture: not found")

	// ErrNoTargets is returned when a start request names no targets at all.
	ErrNoTargets = errors.New("capture: request names no targets")

	// ErrNoAgent is returned when a node-local capture is requested but no
	// capture Agent is wired (a daemon that cannot capture on its own node).
	ErrNoAgent = errors.New("capture: no capture agent configured")

	// ErrNoRemote is returned when a peer-node capture is requested but no
	// remote capturer (peer client) is wired.
	ErrNoRemote = errors.New("capture: no remote capturer configured for peer nodes")
)
