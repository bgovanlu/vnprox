// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// OpExplanation is one staged op's plain-language rendering: what it does,
// read from change.Op's own typed fields (Type, Target, Params) — never
// from a rendered/serialized string. Op.ID is carried through so a caller
// rendering a whole changeset can line an explanation back up with the op
// it came from.
type OpExplanation struct {
	OpID    string
	OpType  string
	Target  string
	Summary string
}

// ExplainOp renders one op's plain-language summary from op.Type (a closed
// vocabulary, decomposed into a verb + object noun phrase by opVerbs/
// opNouns below) and op.Target — enriched, for a handful of ops where a
// generic noun phrase alone would lose the interesting detail, by reading
// op.Params through a type switch (opEnrichers).
//
// This never inspects anything but Op's own typed fields: there is no
// Detail-equivalent string on change.Op to parse, and this package does not
// invent one.
func ExplainOp(op change.Op) OpExplanation {
	verb, noun, ok := decomposeOpType(op.Type)
	summary := ""
	switch {
	case !ok:
		summary = fmt.Sprintf("%s (no plain-language description registered for this op type yet)", op.Type)
	default:
		summary = verb + " " + noun + targetSuffix(op.Target) + opEnrichment(op) + "."
	}
	return OpExplanation{
		OpID:    op.ID,
		OpType:  string(op.Type),
		Target:  targetString(op.Target),
		Summary: summary,
	}
}

// ExplainChangeset renders every op in ops, in order — the "explain this
// changeset's diff" half of the task card, applied to a whole changeset
// rather than one op at a time.
func ExplainChangeset(ops []change.Op) []OpExplanation {
	out := make([]OpExplanation, len(ops))
	for i, op := range ops {
		out[i] = ExplainOp(op)
	}
	return out
}

// opVerbs maps an OpType's final dot-separated segment (docs/data-model.md
// §3's wire vocabulary is consistently "<object>.<verb>") to the
// present-tense verb phrase ExplainOp opens its summary with.
//
//nolint:gochecknoglobals // a read-only vocabulary table, the same shape opNouns below and internal/findings.allCheckNames already are
var opVerbs = map[string]string{
	"create":    "Creates",
	"update":    "Updates",
	"delete":    "Deletes",
	"add":       "Adds",
	"remove":    "Removes",
	"rename":    "Renames",
	"replace":   "Replaces",
	"move":      "Reorders",
	"provision": "Provisions",
	"apply":     "Applies",
}

// opNouns maps an OpType's object prefix (everything before the final
// dot-separated verb segment) to the noun phrase naming what kind of thing
// the op acts on. TestExplainOp_EveryKnownOpTypeIsCovered asserts every
// prefix change.KnownOpTypes() actually uses appears here, the same
// "coverage gate, proven to fail" discipline findings.go's coverageGaps
// uses for check names, mirroring internal/change/preview.go's own
// TestPreview_EveryOpTypeIsProjectedOrDisclosed for the op vocabulary.
//
//nolint:gochecknoglobals // a read-only vocabulary table, the same shape opVerbs above and internal/findings.allCheckNames already are
var opNouns = map[string]string{
	"bond":            "a bond",
	"bridge":          "a bridge",
	"bridge.port":     "a bridge port",
	"fw.alias":        "a firewall alias",
	"fw.group":        "a firewall security group",
	"fw.ipset":        "a firewall IP set",
	"fw.options":      "the firewall options",
	"fw.rule":         "a firewall rule",
	"guest.nic":       "a guest NIC's network binding",
	"iface":           "an interface's configuration",
	"iface.raw":       "the entire /etc/network/interfaces file",
	"ipam.alloc":      "an IPAM address allocation",
	"nat.masquerade":  "a NAT masquerade rule",
	"nat.portforward": "a NAT port-forward rule",
	"qos.shape":       "a QoS bridge shape",
	"route.static":    "a static route",
	"sdn":             "the pending SDN configuration",
	"sdn.controller":  "an SDN controller",
	"sdn.dns.record":  "an SDN DNS record",
	// A /cluster/sdn/dns entry is a PowerDNS server connection, so that is
	// what an operator is told (T-4114). This said "an SDN DNS zone" until
	// the rename, which is the defect: a preview line reading "delete SDN DNS
	// zone pdns1" describes destroying a domain's records when what will
	// actually happen is that a server connection is removed.
	"sdn.dns.server": "a PowerDNS server connection",
	// The retired spelling. change.OpType.Canonical rewrites it at decode, so
	// nothing should reach here with it; it stays mapped because this package
	// also explains ops read straight from historical audit records, which
	// were written before the rename and never pass through that decoder.
	"sdn.dns.zone": "a PowerDNS server connection",
	"sdn.fabric":   "an SDN fabric",
	"sdn.ipam":     "an SDN IPAM plugin instance",
	"sdn.subnet":   "an SDN subnet",
	"sdn.vnet":     "an SDN VNet",
	"sdn.zone":     "an SDN zone",
	"switch.port":  "a managed switch port's configuration",
	"tc.mirror":    "a traffic-mirror session",
	"vf":           "an SR-IOV virtual function",
	"vlan":         "a VLAN",
	"wg.peer":      "a WireGuard peer",
	"wg.tunnel":    "a WireGuard tunnel",
}

// decomposeOpType splits t on its final '.' into a verb segment (looked up
// in opVerbs) and an object prefix (looked up in opNouns). ok is false when
// either half is unrecognized, so a caller never silently renders half a
// sentence for an op type this package hasn't learned about.
func decomposeOpType(t change.OpType) (verb, noun string, ok bool) {
	s := string(t)
	idx := strings.LastIndex(s, ".")
	if idx < 0 {
		return "", "", false
	}
	prefix, suffix := s[:idx], s[idx+1:]
	v, vok := opVerbs[suffix]
	n, nok := opNouns[prefix]
	if !vok || !nok {
		return "", "", false
	}
	return v, n, true
}

// targetSuffix renders op.Target's identity and node scope, generically —
// the same "computed once from typed fields, not templated per op type"
// shape findings.go's whereClause uses for Nodes/Refs. Empty for a
// zero-value Target (the sole no-target op, sdn.apply — see op.go's
// noTargetOps).
func targetSuffix(target inventory.Ref) string {
	if target.IsZero() {
		return ""
	}
	var b strings.Builder
	if target.ID != "" {
		fmt.Fprintf(&b, " %q", target.ID)
	}
	if target.Node != "" {
		fmt.Fprintf(&b, " on node %s", target.Node)
	}
	return b.String()
}

// targetString renders op.Target as OpExplanation.Target: the same
// "kind:node:id" encoding docs/api.md's Ref triplet already uses
// everywhere else, or "" for a no-target op.
func targetString(target inventory.Ref) string {
	if target.IsZero() {
		return ""
	}
	return target.String()
}

// opEnrichment adds op-specific detail a generic noun phrase alone would
// lose, by reading op.Params through a type switch — still a typed field,
// never a rendered string. Only the handful of ops where the interesting
// content lives in Params rather than Target are covered; every other op's
// summary is the generic verb+noun+target sentence alone, which is already
// a complete, honest description of what the op does.
func opEnrichment(op change.Op) string {
	switch p := op.Params.(type) {
	case *change.IfaceRenameParams:
		if p != nil && p.NewName != "" {
			return fmt.Sprintf(" to %q", p.NewName)
		}
	case *change.BridgePortAddParams:
		if p != nil && p.Port != "" {
			return fmt.Sprintf(" (port %q)", p.Port)
		}
	case *change.BridgePortRemoveParams:
		if p != nil && p.Port != "" {
			return fmt.Sprintf(" (port %q)", p.Port)
		}
	case *change.FwRuleMoveParams:
		if p != nil {
			return fmt.Sprintf(" from position %d to %d", p.FromPos, p.ToPos)
		}
	}
	return ""
}
