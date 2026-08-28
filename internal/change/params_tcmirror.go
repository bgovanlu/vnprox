// SPDX-License-Identifier: Apache-2.0

package change

// params_tcmirror.go defines T-4014's tc.mirror.* op parameter structs.
// Every tc.mirror.* op flows through the ordinary stage->validate->diff->
// apply->confirm/rollback changeset lifecycle — there is deliberately no
// second mutation path for a mirror session (CLAUDE.md's change-engine
// invariant; op.go's OpTcMirrorCreate doc comment).
//
// Target Refs (docs/data-model.md §3): tc.mirror.* ops target a tc-mirror
// Ref ({Kind: KindTcMirror, Node: owning node, ID: caller-chosen session
// id}) — the session has no interfaces(5) stanza or other dedicated
// inventory entity of its own, mirroring KindQosShape's identical
// "caller-chosen id, no live-polled entity" shape.
//
// SourceIface/DestIface are plain interface names (like params_qos.go's
// Bridge field, not a nested Ref object) — op.Target.Node already supplies
// the node.

// TcMirrorCreateParams is op "tc.mirror.create". SourceIface is the
// interface whose traffic is copied; DestIface is where the copy is sent
// (both plain, node-local interface names — validate_referential.go
// requires both to exist on op.Target.Node, and validate_safety.go refuses
// a DestIface that names a protected/management interface).
//
// MaxDurationSec is REQUIRED and positive: T-4014's card is explicit that
// a mirror session "must have a maximum duration and must stop itself" —
// unlike internal/capture's Caps (which silently clamp an over-ceiling
// request down to the server's configured maximum), an over-ceiling
// MaxDurationSec here is a hard validate-time rejection (codeTcMirrorCap,
// validate_safety.go) — the task card's own distinction ("rejected... not
// silently clamped"). See internal/change/tcmirror_expiry.go for how the
// bound is actually enforced.
//
// MaxMbit is an OPTIONAL declared bandwidth ceiling, checked against the
// server's configured aggregate-per-node ceiling at validate time
// (validate_safety.go) — it is never rendered as a kernel `police` action
// (internal/tcmirror's doc comment explains why: policing the SOURCE's
// clsact filter chain would drop real production traffic, not just the
// mirrored copy).
type TcMirrorCreateParams struct {
	MaxMbit        *int   `json:"maxMbit,omitempty"`
	SourceIface    string `json:"sourceIface"`
	DestIface      string `json:"destIface"`
	MaxDurationSec int    `json:"maxDurationSec"`
}

func (TcMirrorCreateParams) isChangeParams() {}

// TcMirrorUpdateParams is op "tc.mirror.update": the ONLY mutable field is
// MaxDurationSec — re-arming a session's own expiry deadline (extending or
// shortening it). SourceIface/DestIface are immutable after create (like
// nat.*/route.static.*'s "no update" rule for identity-changing fields,
// op.go's OpNatMasqueradeCreate doc comment): re-pointing a mirror session
// at a different source or destination is a delete-and-recreate, a fresh,
// individually-auditable session, not a silent re-scope of an existing
// one. Because SourceIface/DestIface never change, tc.mirror.update never
// re-renders any tc state at all (internal/tcmirror.RenderTC is not called
// for it) — it is pure store-side bookkeeping
// (store.TcMirrorSessionRepo.UpdateDuration).
type TcMirrorUpdateParams struct {
	MaxDurationSec *int `json:"maxDurationSec,omitempty"`
}

func (TcMirrorUpdateParams) isChangeParams() {}

// TcMirrorDeleteParams is op "tc.mirror.delete". It carries no params —
// tearing down a session's clsact qdisc/filters needs only the target's
// own id (internal/tcmirror.RenderTCTeardown re-derives SourceIface from
// the stored session row).
type TcMirrorDeleteParams struct{}

func (TcMirrorDeleteParams) isChangeParams() {}
