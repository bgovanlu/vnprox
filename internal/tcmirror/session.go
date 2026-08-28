// SPDX-License-Identifier: Apache-2.0

package tcmirror

import "fmt"

// Session is one SPAN/mirror session (T-4014): the params of a
// tc.mirror.create op (internal/change's TcMirrorCreateParams) plus the
// caller-chosen id that also names the op's target
// (inventory.KindTcMirror Ref).
//
// SourceIface and DestIface are plain interface names on the same node —
// op.Target.Node already supplies the node, so a second nested Ref here
// would be a redundant encoding of the same node twice (the same
// convention params_qos.go's Bridge field and params_edge.go's Iface
// field already use). MaxMbit is a DECLARED bandwidth ceiling used only
// for validate-time cap checking (internal/change/validate_safety.go) —
// see doc.go's "what this package deliberately does NOT do" for why it is
// never rendered as a kernel `police` action.
type Session struct {
	ID          string
	Node        string
	SourceIface string
	DestIface   string
	MaxMbit     int
}

// RenderTC renders the ordered `tc` argv lines (each a full command,
// argv[0] == "tc") that realize s:
//
//  1. `tc qdisc add dev <source> clsact` — the clsact qdisc, which exposes
//     both an ingress and an egress hook on the source interface without
//     disturbing whatever root qdisc already governs its normal forwarding
//     path (see doc.go's pvecube evidence: clsact never collides with the
//     mq/noqueue/fq_codel root qdiscs observed there).
//  2. `tc filter add dev <source> ingress protocol all prio 1 matchall
//     action mirred egress mirror dev <dest>` — mirrors every packet
//     ENTERING the source interface.
//  3. `tc filter add dev <source> egress protocol all prio 1 matchall
//     action mirred egress mirror dev <dest>` — mirrors every packet
//     LEAVING the source interface, so a SPAN of one port captures both
//     directions of its traffic, matching conventional switch port-mirror
//     semantics.
//
// Both filter lines use `add`, not `replace`: a Session's clsact qdisc and
// filters are created exactly once, at tc.mirror.create apply time — there
// is no in-place re-render (tc.mirror.update only ever changes
// MaxDurationSec, a pure store-side bookkeeping field with no tc-visible
// effect; see internal/change/apply_tcmirror.go). Re-applying RenderTC's
// qdisc line for an already-clsact'd source is intentionally still `add`,
// not `replace`, so a second, unrelated attempt to mirror the same source
// (validate_referential.go's duplicate-target check) fails loudly at the
// tc layer too, rather than one session's teardown silently discarding the
// other's filters.
func RenderTC(s Session) ([][]string, error) {
	if s.SourceIface == "" {
		return nil, fmt.Errorf("tcmirror: session %s: sourceIface is required", s.ID)
	}
	if s.DestIface == "" {
		return nil, fmt.Errorf("tcmirror: session %s: destIface is required", s.ID)
	}
	if s.SourceIface == s.DestIface {
		return nil, fmt.Errorf("tcmirror: session %s: destIface must differ from sourceIface (got %q for both)", s.ID, s.SourceIface)
	}
	return [][]string{
		{"tc", "qdisc", "add", "dev", s.SourceIface, "clsact"},
		{"tc", "filter", "add", "dev", s.SourceIface, "ingress", "protocol", "all", "prio", "1", "matchall", "action", "mirred", "egress", "mirror", "dev", s.DestIface},
		{"tc", "filter", "add", "dev", s.SourceIface, "egress", "protocol", "all", "prio", "1", "matchall", "action", "mirred", "egress", "mirror", "dev", s.DestIface},
	}, nil
}

// RenderTCTeardown renders the ordered `tc` argv lines that remove s from
// its source interface — the inverse of RenderTC. Unlike
// internal/qos.RenderTCTeardown (which deliberately leaves a bridge's
// shared HTB root qdisc in place because sibling shapes may still use it),
// a source interface's clsact qdisc is owned exclusively by its one mirror
// session (validate_referential.go rejects a second tc.mirror.create
// targeting an already-mirrored source), so teardown safely removes the
// qdisc itself too, leaving no idle tc state behind.
func RenderTCTeardown(s Session) [][]string {
	return [][]string{
		{"tc", "filter", "del", "dev", s.SourceIface, "ingress"},
		{"tc", "filter", "del", "dev", s.SourceIface, "egress"},
		{"tc", "qdisc", "del", "dev", s.SourceIface, "clsact"},
	}
}
