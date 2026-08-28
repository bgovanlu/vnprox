// SPDX-License-Identifier: Apache-2.0

package change

// params_qos.go defines T-1505's QoS op parameter structs. Every qos.* op
// flows through the ordinary stage→validate→diff→apply→confirm/rollback
// changeset lifecycle — there is deliberately no second mutation path for
// traffic shaping (CLAUDE.md's change-engine invariant).
//
// Target Refs (docs/data-model.md §3): qos.shape.* ops target a qos-shape
// Ref ({Kind: KindQosShape, Node: owning node, ID: caller-chosen shape id})
// — the shape has no interfaces(5) stanza or other dedicated inventory
// entity of its own, mirroring KindNatRule/KindStaticRoute's identical
// "caller-chosen id, no live-polled entity" shape.
//
// Bridge is the bridge's plain interface name (like nat.*/route.static.*'s
// Iface field, not a nested Ref object) — op.Target.Node already supplies
// the node, so a second nested {kind,node,id} Ref here would be a
// redundant encoding of the same node twice.

// QosShapeCreateParams is op "qos.shape.create". Bridge is the bridge this
// shape governs; MatchCIDR/MatchVlan optionally narrow it to a subset of
// the bridge's traffic (both empty/nil == the shape governs the bridge's
// whole, otherwise-unclassified egress — internal/qos.RenderTC's doc
// comment). RateMbit is required and positive; CeilMbit, when set, must be
// >= RateMbit (validate_schema.go).
type QosShapeCreateParams struct {
	MatchVlan *int   `json:"matchVlan,omitempty"`
	CeilMbit  *int   `json:"ceilMbit,omitempty"`
	Priority  *int   `json:"priority,omitempty"`
	Bridge    string `json:"bridge"`
	MatchCIDR string `json:"matchCidr,omitempty"`
	RateMbit  int    `json:"rateMbit"`
}

func (QosShapeCreateParams) isChangeParams() {}

// QosShapeUpdateParams is op "qos.shape.update": pointer fields are set
// only for the attributes being changed (nil == leave unchanged), the same
// partial-patch convention every other *UpdateParams struct in this
// package uses. MatchCIDR/MatchVlan/CeilMbit/Priority use the package's
// standard "absent means unchanged" pointer semantics — there is
// deliberately no separate "clear this match" flag (a shape's match
// narrowing is expected to be set once at create time; changing which
// traffic a shape selects is a delete-and-recreate, not an in-place
// re-scope, mirroring T-1401/T-1403's identical "identity-changing edit is
// two visible ops" precedent elsewhere in this op vocabulary).
type QosShapeUpdateParams struct {
	Bridge    *string `json:"bridge,omitempty"`
	MatchCIDR *string `json:"matchCidr,omitempty"`
	MatchVlan *int    `json:"matchVlan,omitempty"`
	RateMbit  *int    `json:"rateMbit,omitempty"`
	CeilMbit  *int    `json:"ceilMbit,omitempty"`
	Priority  *int    `json:"priority,omitempty"`
}

func (QosShapeUpdateParams) isChangeParams() {}

// QosShapeDeleteParams is op "qos.shape.delete". It carries no params —
// removing a shape's tc/HTB class/filter needs only the target's own id
// (internal/qos.RenderTCTeardown re-derives the classid from it).
type QosShapeDeleteParams struct{}

func (QosShapeDeleteParams) isChangeParams() {}
