package change

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// withHostWriteActor stamps T-2902 attribution — the acting username and
// the client IP internal/api's auditIPMiddleware put in ctx — for the peer
// client to carry on any host write this operation fans out to a remote
// node. ClusterNodeAgent.withOrigin adds the coordinator's node name at the
// moment a call actually goes remote; system-initiated paths (confirm-timer
// rollbacks) never pass here and are recorded remotely as unattributed,
// which is the truth.
func withHostWriteActor(ctx context.Context, author string) context.Context {
	a := peer.AttributionFromContext(ctx)
	a.Actor = author
	a.OriginIP = store.AuditClientIPFromContext(ctx)
	return peer.WithAttribution(ctx, a)
}

// ValidatePeerHostWrite is T-2902's receiving-side guard: the safety
// verdict this node's own change engine would give to staging content as
// its /etc/network/interfaces, expressed as the blocking findings (empty
// means safe to write).
//
// The peer routes (`/api/peer/host/stage-interfaces`, `/host/restore`)
// historically handed registry-of-record content straight to the host
// writer — every interlock docs/security.md documents as absolute lived
// only on the *coordinating* node, so anything holding the cluster secret
// had a validation-free path to this node's network config. This method
// closes that by running the exact pipeline the coordinator runs — the
// same one the raw-editor escape hatch uses (expand the full-file content
// into per-entity delta ops against the live file, then every validator
// class including the protected-interface interlocks and the declarative
// policy set) — so a peer write is refused under precisely the conditions
// a local changeset would be. Parity is the point: there is one safety
// standard, not a stricter local one and a looser remote one.
//
// BaseHash is deliberately empty: the editor's stale-read conflict guard
// answers "did someone else edit the file since you looked", which is the
// coordinator's concern; this node's concern is only "does the resulting
// state strand my management path". An empty protected set (onboarding
// never confirmed one here) checks nothing, exactly as it does for local
// changesets — defense in depth, not a new gate the coordinator lacks.
func (s *Service) ValidatePeerHostWrite(ctx context.Context, node, content string) []string {
	ops := []Op{{
		Type:   OpIfaceRawReplace,
		Target: inventory.Ref{Kind: inventory.KindNode, Node: node, ID: node},
		Params: &IfaceRawReplaceParams{Content: content},
	}}
	var out []string
	for _, f := range s.validate(ctx, "", ops) {
		if f.Severity != SeverityError {
			continue
		}
		out = append(out, f.Message)
	}
	return out
}
