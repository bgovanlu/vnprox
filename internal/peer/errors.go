package peer

import (
	"errors"
	"fmt"
)

// ErrPeerUnreachable is the caller-visible sentinel for a peer that could
// not be reached at all (connection refused/timed out, or its circuit
// breaker is currently open) — docs/api.md's documented `peer_unreachable`
// error code. Client methods wrap it with %w so callers can
// errors.Is(err, peer.ErrPeerUnreachable).
var ErrPeerUnreachable = errors.New("peer_unreachable")

// ErrPeerIncompatible indicates a peer's advertised protocol version
// (VersionInfo.ProtocolVersion) does not match this daemon's, per
// docs/architecture.md §5: "a daemon refuses to coordinate changes
// involving a peer with an incompatible schema version". Callers should
// surface this as the documented upgrade-prompt refusal.
var ErrPeerIncompatible = errors.New("peer_incompatible")

// ErrNoSecret indicates the peer server or client has no cluster secret
// loaded (secret file missing/unreadable/empty) and can therefore neither
// sign nor verify peer requests.
var ErrNoSecret = errors.New("peer: no cluster secret loaded")

// ResponseError is returned by Client methods when a peer responded (so the
// circuit breaker treats it as a live peer) but with a non-2xx status —
// e.g. a 401 (secret mismatch/rotation skew), 404 (unknown node on that
// peer), or a host-write failure. Code/Message come from the peer's own
// error envelope ({"error":{"code","message"}}, docs/api.md's convention)
// when parseable, or a generic fallback otherwise.
type ResponseError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("peer: request failed (status %d, code %s): %s", e.StatusCode, e.Code, e.Message)
}
