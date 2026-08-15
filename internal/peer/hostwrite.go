package peer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// This file is T-2902: the receiving side of the `/api/peer/host/*` write
// routes stops being a validation-free, audit-free shortcut past the
// change engine's interlocks. Two seams, both declared here (not against
// internal/change or internal/store — the same import-direction discipline
// HostWriter/AuditReader already follow; cmd/vnproxd adapts the concrete
// implementations):
//
//   - HostWriteGuard: the receiving node's own safety verdict on content
//     about to become its /etc/network/interfaces, plus the provenance
//     check that keeps distributed rollback working.
//   - HostWriteAuditor: an audit row on the RECEIVING node for every host
//     write — allowed, refused, or failed — with the originating
//     attribution the request carried. Until T-2902 these writes appeared
//     in no audit log anywhere except the coordinator's changeset trail.
//
// Both are optional in the nil-safe tradition of every other
// ServerOptions dependency — but unlike the others, absence here weakens a
// documented guarantee rather than dark-launching a feature, so
// NewServer's production wiring (cmd/vnproxd) always sets both, and
// handleStageInterfaces logs a warning per write when the guard is absent
// rather than silently reverting to the pre-T-2902 behavior.

// HostWriteGuard is the receiving-side safety check for peer host writes.
// cmd/vnproxd adapts internal/change.Service.ValidatePeerHostWrite (the
// exact validator pipeline a local changeset runs) and
// store.SnapshotRepo.HasFileHash to this shape.
type HostWriteGuard interface {
	// ValidateStagedContent returns the blocking findings staging content
	// as node's interfaces file would produce — empty means safe. A
	// non-empty return refuses the write with the findings on the wire.
	ValidateStagedContent(ctx context.Context, node, content string) []string

	// KnownContent reports whether content matches a file some snapshot on
	// this node already records for node — the provenance under which a
	// restore is a rollback to a known-good state (exempt from
	// ValidateStagedContent, which would otherwise refuse a legitimate
	// restore that re-arms the management path) rather than a fresh write.
	KnownContent(ctx context.Context, node, content string) bool
}

// HostWriteAudit is one receiving-side audit record for a peer host write.
type HostWriteAudit struct {
	// Action is the peer.host.* action name, e.g. "peer.host.stage".
	Action string
	// Node is the target node of the write (this daemon's own node in the
	// production topology — the coordinator dialed us because it is ours).
	Node string
	// Actor / OriginNode / OriginIP are the coordinating daemon's account
	// of who asked, carried in the request body (writeAttribution). Empty
	// when an older coordinator sent no attribution — recorded as such,
	// never invented.
	Actor      string
	OriginNode string
	OriginIP   string
	// Result is "allowed", "refused" (guard findings), or "failed" (the
	// writer returned an error after the guard passed).
	Result string
	// Detail carries the refusal findings or the writer error; "" on
	// success.
	Detail string
	// ContentSHA256 is the hex digest of the content written (stage,
	// restore), or "" for the content-free actions (ifreload, discard,
	// lldp-install).
	ContentSHA256 string
	// Provenance is "" or "snapshot" — whether KnownContent exempted a
	// restore from validation, so the audit trail says *why* a
	// management-path-re-arming write was allowed through.
	Provenance string
}

// HostWriteAuditor records receiving-side host-write audit rows.
// Implementations must not block the write path on audit storage errors —
// log and continue, exactly like every other append site in this codebase.
type HostWriteAuditor interface {
	AppendHostWrite(ctx context.Context, e HostWriteAudit)
}

// Attribution is the client-side half of writeAttribution: who this
// coordinating daemon says is behind the host writes it is about to fan
// out. Carried by context (WithAttribution) rather than by widening every
// Client method signature, because the write methods are called through
// internal/change's NodeAgent interfaces, which must stay attribution-
// agnostic — the change engine's job is the mutation, the transport's job
// is saying who asked.
type Attribution struct {
	Actor      string
	OriginNode string
	OriginIP   string
}

type attributionKey struct{}

// WithAttribution returns ctx carrying a for the Client's host-write
// methods to stamp into their request bodies. cmd/vnproxd's NodeAgent
// adapter sets it once per call from the session identity, the local node
// name, and the audit client IP already in the request context.
func WithAttribution(ctx context.Context, a Attribution) context.Context {
	return context.WithValue(ctx, attributionKey{}, a)
}

// AttributionFromContext returns the Attribution WithAttribution stored,
// or the zero value — read by the Client's write methods and by
// internal/change's ClusterNodeAgent, which merges in the coordinator's
// node name at the moment a call goes remote.
func AttributionFromContext(ctx context.Context) Attribution {
	a, _ := ctx.Value(attributionKey{}).(Attribution)
	return a
}

// contentSHA256 is the digest stamped into HostWriteAudit.ContentSHA256 and
// compared by KnownContent implementations — one definition so the audit
// trail and the provenance check can never disagree about what was hashed.
func contentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// auditHostWrite is the handlers' nil-safe append helper.
func (s *Server) auditHostWrite(ctx context.Context, e HostWriteAudit) {
	if s.opts.WriteAudit == nil {
		return
	}
	s.opts.WriteAudit.AppendHostWrite(ctx, e)
}
