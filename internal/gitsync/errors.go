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

	// ErrRemoteNotFound is wrapped alongside ErrRemoteStatus for a 404. The
	// write path (T-2702) branches on it constantly and the difference is
	// never cosmetic: "this branch does not exist yet" means create it,
	// "this host refused us" means abandon the proposal without having
	// changed anything.
	ErrRemoteNotFound = errors.New("gitsync: remote resource not found")

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

	// --- T-2702, the propose path -------------------------------------

	// ErrProposeNotConfigured is returned by Propose when this deployment has
	// no write-capable host wired ([gitsync] push_token_file unset, or
	// gitsync off entirely). Nothing is contacted and nothing is written.
	ErrProposeNotConfigured = errors.New("gitsync: proposing changesets is not configured")

	// ErrNoProposal is returned when a changeset has never been proposed. It
	// is a plain answer, not a failure: most changesets never are.
	ErrNoProposal = errors.New("gitsync: this changeset has not been proposed")

	// ErrNothingToPropose is returned for a changeset with no ops, and for
	// one whose ops make no difference to the spec document as it stands
	// (T-2702 AC2's "a changeset with an empty diff cannot be proposed").
	ErrNothingToPropose = errors.New("gitsync: there is nothing to propose")

	// ErrNotProposable is returned for a changeset whose ops were abandoned
	// or undone (discarded, rolled back, failed): they are not a statement of
	// intent and must not become one.
	ErrNotProposable = errors.New("gitsync: this changeset cannot be proposed")

	// ErrNotExpressible is returned when a changeset contains an op the
	// declarative spec has no vocabulary for — every delete, and every
	// firewall/IPAM/QoS/WireGuard/raw-file op. It wraps internal/spec's own
	// ErrOpNotExpressible/ErrTargetNotInSpec, which name the offending op.
	ErrNotExpressible = errors.New("gitsync: this changeset cannot be expressed in the spec")

	// ErrRoundTrip is returned when the proposed document would NOT re-import
	// to the changeset's own ops (T-2702 AC1). It is the guard that stops a
	// pull request meaning something other than the changeset it came from,
	// and it is checked before anything is written.
	ErrRoundTrip = errors.New("gitsync: the proposed spec does not round-trip to this changeset")

	// ErrNoSpecDocument is returned when the repository has no document at
	// the configured path. Inventing a whole-cluster spec as a side effect of
	// proposing one edit would adopt all of live state into intent at once,
	// which is a human's explicit decision (T-2703), never this path's.
	ErrNoSpecDocument = errors.New("gitsync: the repository has no spec document at the configured path")

	// ErrNotConfigured is returned by a Sync call on a disabled or
	// incompletely configured Service. Nothing is fetched and nothing is
	// written; it exists so a caller that wires the service unconditionally
	// (as cmd/vnproxd does) gets a clear answer rather than a nil-pointer.
	ErrNotConfigured = errors.New("gitsync: not configured")
)
