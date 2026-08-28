// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
)

// CorrelationStatus honestly names why (or whether) a log Entry could be
// tied to a specific configured rule — see doc.go for why "uncorrelatable,
// here's exactly why" is the right contract rather than ever guessing.
type CorrelationStatus string

const (
	// StatusRule: exactly one enabled rule in the guest's resolved
	// evaluation order matches this line's direction/action (optionally
	// narrowed by protocol/port) — Rule is the deep-link target.
	StatusRule CorrelationStatus = "rule"
	// StatusDefaultPolicy: the line matched a default-policy fallthrough
	// (pvefw-logger's "policy $policy: " message), not any specific rule —
	// there is nothing to deep-link to by design, not by parsing failure.
	StatusDefaultPolicy CorrelationStatus = "default_policy"
	// StatusAmbiguous: 2+ enabled rules match direction+action (and
	// proto/port narrowing, if applicable, didn't reduce it to one); real
	// pve-firewall's log format doesn't carry a rule position (doc.go), so
	// this is reported honestly rather than picked arbitrarily.
	StatusAmbiguous CorrelationStatus = "ambiguous"
	// StatusUnmatched: no enabled rule in the resolved order matches this
	// line's direction/action at all (e.g. the ruleset changed since the
	// line was logged, or the action was undeterminable from the message).
	StatusUnmatched CorrelationStatus = "unmatched"
	// StatusUnknownChain: the line's chain isn't a recognized guest
	// tap/veth chain (see doc.go's "Scope: guest chains only") — never
	// attempted.
	StatusUnknownChain CorrelationStatus = "unknown_chain"
	// StatusNoGuestData: the chain names a guest this daemon has not yet
	// observed any firewall ruleset for.
	StatusNoGuestData CorrelationStatus = "no_guest_data"
)

// RuleRef names one rule in a guest's resolved evaluation order — enough
// to deep-link to it robustly (by ref+pos+origin, not by DOM position; see
// this task's completion report on why that survives the firewall UI's
// independent evolution) without this package depending on web routing at
// all.
type RuleRef struct {
	GuestRef  string // inventory.Ref.String(), e.g. "guest:pve1:117"
	Origin    string // fw.Origin: "cluster" | "group" | "guest"
	GroupName string // set iff Origin == "group"
	Pos       int    // ResolvedRule.Pos — stable position in the resolved order
}

// Correlation is Correlate's result for one Entry.
type Correlation struct {
	Rule *RuleRef // set iff Status == StatusRule

	Status CorrelationStatus
	Reason string // always set for every non-StatusRule status: the honest "why not"

	// CandidatePositions lists the resolved-order Pos of every rule that
	// matched direction+action (post proto/port narrowing) when Status ==
	// StatusAmbiguous — informational only ("possibly one of rules #3,
	// #7"), never presented as a confident answer.
	CandidatePositions []int
}

// Correlate maps a parsed guest-chain log Entry to the rule in resolved
// (that guest's fw.Resolve output) that most likely produced it. See
// doc.go for the grounding and honesty contract; guardRailed by design to
// never claim StatusRule unless disambiguation is unambiguous.
func Correlate(e Entry, resolved fw.ResolvedView) Correlation {
	if !e.Guest {
		return Correlation{
			Status: StatusUnknownChain,
			Reason: fmt.Sprintf("chain %q is not a recognized guest tap/veth chain; node/cluster-scope chain correlation is not evaluated (see internal/fwlog's doc comment)", e.Chain),
		}
	}
	if e.PolicyFallthrough {
		dir := e.Direction
		if dir == "" {
			dir = "unknown-direction"
		}
		return Correlation{
			Status: StatusDefaultPolicy,
			Reason: fmt.Sprintf("matched the %s default policy fallthrough, not a specific rule", dir),
		}
	}
	if e.Action == "" {
		return Correlation{Status: StatusUnmatched, Reason: "could not determine the matched action from the log message"}
	}

	var candidates []fw.ResolvedRule
	for _, rr := range resolved.Rules {
		if !rr.Rule.Enabled {
			continue
		}
		if rr.Rule.Direction != e.Direction {
			continue
		}
		if !strings.EqualFold(rr.Rule.Action, e.Action) {
			continue
		}
		candidates = append(candidates, rr)
	}
	if len(candidates) == 0 {
		return Correlation{
			Status: StatusUnmatched,
			Reason: fmt.Sprintf("no enabled rule in this guest's resolved order matches direction=%s action=%s", e.Direction, e.Action),
		}
	}

	if len(candidates) > 1 {
		candidates = narrowByProtoPort(candidates, e)
	}

	if len(candidates) == 1 {
		c := candidates[0]
		return Correlation{Status: StatusRule, Rule: &RuleRef{
			GuestRef: resolved.Guest.String(), Origin: string(c.Origin), GroupName: c.GroupName, Pos: c.Pos,
		}}
	}

	positions := make([]int, len(candidates))
	for i, c := range candidates {
		positions[i] = c.Pos
	}
	return Correlation{
		Status:             StatusAmbiguous,
		CandidatePositions: positions,
		Reason: fmt.Sprintf(
			"%d rules match direction=%s action=%s; pve-firewall's log format does not include a rule position, so which one logged this line can't be determined",
			len(candidates), e.Direction, e.Action,
		),
	}
}

// narrowByProtoPort best-effort-narrows an ambiguous candidate set using
// the packet's own logged protocol/destination-port against each
// candidate rule's configured Proto/Dport, when both sides specify one. A
// dimension is only applied if doing so leaves at least one candidate — a
// rule with no Proto/Dport set (a wildcard, or a macro-driven rule whose
// ports come from macro expansion rather than its own fields — macro-based
// narrowing is not evaluated here) always remains a candidate, since
// eliminating it could discard the actual match. This is intentionally a
// heuristic, not a re-simulation of pve-firewall's evaluation order (that
// is internal/sim's job, T-503) — it only ever narrows, never invents a
// match Correlate's caller would present as certain.
func narrowByProtoPort(candidates []fw.ResolvedRule, e Entry) []fw.ResolvedRule {
	out := candidates
	if e.Proto != "" {
		if filtered := filterByProto(out, e.Proto); len(filtered) > 0 {
			out = filtered
		}
	}
	if e.Dport != "" {
		if filtered := filterByDport(out, e.Dport); len(filtered) > 0 {
			out = filtered
		}
	}
	return out
}

func filterByProto(candidates []fw.ResolvedRule, proto string) []fw.ResolvedRule {
	var out []fw.ResolvedRule
	for _, c := range candidates {
		if c.Rule.Proto == "" || strings.EqualFold(c.Rule.Proto, proto) {
			out = append(out, c)
		}
	}
	return out
}

func filterByDport(candidates []fw.ResolvedRule, dport string) []fw.ResolvedRule {
	var out []fw.ResolvedRule
	for _, c := range candidates {
		if c.Rule.Dport == "" || dportMatches(c.Rule.Dport, dport) {
			out = append(out, c)
		}
	}
	return out
}

// dportMatches reports whether logged (a single numeric port, e.g. "443")
// falls within configured — pve-firewall's dport syntax: a bare port
// ("443"), a comma-separated list ("80,443"), or a colon range ("8000:8100").
// Any entry that isn't cleanly one of those forms (e.g. an alias/service
// name this package doesn't resolve) is conservatively treated as "does
// not narrow" (returns false, so it never wrongly eliminates a real
// candidate) rather than guessed at.
func dportMatches(configured, logged string) bool {
	loggedN, err := parsePort(logged)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(configured, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, ":"); ok {
			loN, errL := parsePort(lo)
			hiN, errH := parsePort(hi)
			if errL == nil && errH == nil && loggedN >= loN && loggedN <= hiN {
				return true
			}
			continue
		}
		if n, err := parsePort(part); err == nil && n == loggedN {
			return true
		}
	}
	return false
}

func parsePort(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err
}
