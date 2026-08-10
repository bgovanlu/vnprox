package gitsync

import "errors"

// Sentinel errors for this package, per docs/development.md's "sentinel
// errors in each package's errors.go" convention. Every one of them is a
// condition that must degrade to a finding plus a retry, never to a daemon
// that fails to start (T-2701 AC7).
var (
	// ErrUnreachable is returned when the remote could not be contacted at
	// all — DNS, dial, TLS or timeout. Distinguished from a well-formed HTTP
	// error response so an operator can tell a broken network from a wrong
	// URL, the same distinction internal/peer draws between peer_unreachable
	// and peer_untrusted.
	ErrUnreachable = errors.New("gitsync: remote unreachable")

	// ErrRemoteStatus is returned when the remote answered with a non-2xx
	// status. The finding names the status; the response body is never
	// echoed, because a hosting provider's error body can quote the request
	// (and therefore the credential) back at us.
	ErrRemoteStatus = errors.New("gitsync: remote returned an error status")

	// ErrSpecParse is returned when the fetched document is not a spec this
	// daemon understands. It is deliberately distinct from every transport
	// error: an unparseable spec must leave an existing draft untouched
	// (T-2701 AC4), which is a different response from "we could not fetch".
	ErrSpecParse = errors.New("gitsync: spec does not parse")

	// ErrUnsigned is returned, when require_signed_commits is set, for a
	// commit that carries no signature at all.
	ErrUnsigned = errors.New("gitsync: commit is not signed")

	// ErrUnverifiableSignature is returned, when require_signed_commits is
	// set, for a commit whose signature exists but cannot be verified: an
	// unsupported signature format, a malformed blob, or a signer absent
	// from the operator's allowed-signers file. It is the fail-closed half
	// of the gate — "we could not check" is never treated as "it is fine".
	ErrUnverifiableSignature = errors.New("gitsync: commit signature could not be verified")

	// ErrNotConfigured is returned by a Sync call on a disabled or
	// incompletely configured Service. Nothing is fetched and nothing is
	// written; it exists so a caller that wires the service unconditionally
	// (as cmd/vnproxd does) gets a clear answer rather than a nil-pointer.
	ErrNotConfigured = errors.New("gitsync: not configured")
)
