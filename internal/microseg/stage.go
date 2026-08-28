// SPDX-License-Identifier: Apache-2.0

package microseg

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Stage turns a Proposal into ordinary changeset ops — the planner's ONLY write
// path, and a draft one: it emits fw.rule.create ops (docs/data-model.md §3's
// existing op vocabulary, no new op type) targeting the subject's firewall
// ruleset, in the proposal's own rule order. It never calls
// change.Service.Apply/Confirm; the returned ops are handed to
// change.Service.Create as a DRAFT by the caller, and a human reviews and
// applies them through the ordinary changeset lifecycle (T-1603's UX). This
// package imports internal/change only for these op-construction types — a
// boundary importboundary_test.go enforces.
//
// The whole default-deny policy (the ACCEPT allow-list plus one trailing
// match-all deny per governed direction) is expressed as fw.rule.create ops
// alone, so no fw.options.update / default-policy op is needed and the staged
// changeset is exactly what DryRun evaluated.
func Stage(prop Proposal) []change.Op {
	ops := make([]change.Op, 0, len(prop.Rules))
	for _, r := range prop.Rules {
		ops = append(ops, change.Op{
			Type:   change.OpFwRuleCreate,
			Target: prop.Subject.RulesetRef,
			Params: &change.FwRuleCreateParams{
				Direction: r.Direction,
				Action:    r.Action,
				Proto:     r.Proto,
				Source:    r.Source,
				Dest:      r.Dest,
				Sport:     r.Sport,
				Dport:     r.Dport,
				Macro:     r.Macro,
				Comment:   r.Comment,
				Pos:       r.Pos,
				Enabled:   r.Enabled,
			},
		})
	}
	return ops
}

// GuestRulesetRef builds the firewall-ruleset Ref for a guest whose vmid and
// kind ("qemu"|"lxc") are known — the "guest/<kind>/<vmid>" convention
// internal/fw and the fw.* apply path use (internal/change/apply_helpers). It
// is a caller convenience for wiring a Subject.RulesetRef, kept here so the one
// place that knows the ruleset-id shape for a guest is next to Stage. microseg
// itself never calls it (it takes the ruleset ref ready-made in the Subject),
// because the planner works off flow records whose guest refs carry no
// qemu/lxc kind.
func GuestRulesetRef(node, guestKind, vmid string) inventory.Ref {
	return inventory.Ref{
		Kind: inventory.KindFwRuleset,
		Node: node,
		ID:   "guest/" + guestKind + "/" + vmid,
	}
}
