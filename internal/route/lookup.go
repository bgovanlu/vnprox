// SPDX-License-Identifier: Apache-2.0

package route

import (
	"fmt"
	"net/netip"
	"sort"
)

// LookupResult is the answer to T-3903's core operator question — "which
// path would this address take" — for one destination from one node's
// point of view.
type LookupResult struct {
	// Dst is the destination Lookup was run for, verbatim.
	Dst string
	// MatchedRoute is the FIB route Lookup selected, or nil when
	// Reachable is false.
	MatchedRoute *FIBRoute
	// MatchedRule is the policy rule whose table produced MatchedRoute,
	// or nil when no policy rule was involved (defensive fallback path —
	// see Lookup's doc comment) or when Reachable is false.
	MatchedRule *PolicyRule
	// Trace is a human-readable, ordered account of how Lookup reached
	// its answer (which rule matched, which table it searched, what it
	// found there) — the route-explorer UI's "why" panel renders this
	// directly rather than re-deriving it.
	Trace []string
	// Ambiguous names the candidate device names when more than one
	// equally-specific route matched and no IfaceHint was given to break
	// the tie (the same situation `ip route get <addr>` resolves by
	// requiring an explicit `dev`, e.g. for a link-local IPv6
	// destination reachable via more than one interface — see
	// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt's
	// `ip -j -6 route get fe80::1 dev vmbr0` entry). Reachable is false
	// whenever this is non-empty.
	Ambiguous []string
	// RulesSkipped names any policy rule Lookup could not evaluate
	// because it selects on something Lookup does not model — an fwmark,
	// an incoming interface (`iif`), or a specific source prefix other
	// than "all" (Lookup answers "which path does a destination take",
	// not "...from this specific source", so a source-prefix-scoped rule
	// is reported here rather than silently treated as always-matching
	// or always-skipped). Every rule in this task's evidence transcript
	// is a plain "from all lookup <table>" rule, so this is empty for
	// every node this project has actually observed; it exists so a
	// future VRF-lite/policy-routing configuration degrades into a
	// visible caveat instead of a silently wrong answer.
	RulesSkipped []string
	Reachable    bool
}

// Lookup finds the path traffic to dst would take, evaluating rules in
// ascending priority order exactly as the kernel does (ip-rule(8): "Rules
// are ordered by priority... the first matching rule terminates the rule
// list traversal") and, for the first rule whose selector Lookup can
// evaluate (see RulesSkipped's doc comment) and whose table contains a
// matching route, running longest-prefix-match (ties broken by lowest
// metric, per the kernel's own tie-break) within that table.
//
// ifaceHint, when non-empty, restricts candidate routes to that device
// before ranking — the Lookup-level equivalent of `ip route get <dst> dev
// <iface>`, needed whenever more than one route could equally match (most
// commonly an IPv6 link-local destination, on-link via every interface at
// once with no gateway to disambiguate — exactly the case the evidence
// transcript's `ip route get` probes hit). When ifaceHint is empty and a
// genuine tie exists, Lookup does not guess: it returns Reachable=false
// with Ambiguous naming the tied devices, the same "you must specify a
// device" contract `ip route get` itself enforces for a link-local
// destination.
//
// dst must be a bare address (e.g. "10.0.0.5" or "fe80::1"), not a CIDR —
// this answers "where would a packet TO this address go," matching how an
// operator actually poses the question and how `ip route get` itself is
// invoked.
func Lookup(fib []FIBRoute, rules []PolicyRule, dst string, ifaceHint string) (LookupResult, error) {
	addr, err := netip.ParseAddr(dst)
	if err != nil {
		return LookupResult{}, fmt.Errorf("route: lookup: %q is not a valid IP address: %w", dst, err)
	}
	afi := AFIv4
	if addr.Is6() && !addr.Is4In6() {
		afi = AFIv6
	}

	res := LookupResult{Dst: dst}

	sortedRules := append([]PolicyRule(nil), rules...)
	sort.Slice(sortedRules, func(i, j int) bool { return sortedRules[i].Priority < sortedRules[j].Priority })

	for i := range sortedRules {
		rule := sortedRules[i]
		if rule.AFI != afi {
			continue
		}
		if rule.Src != "" && rule.Src != "all" {
			res.RulesSkipped = append(res.RulesSkipped,
				fmt.Sprintf("priority %d: from %s lookup %s (source-scoped rule, not evaluated)", rule.Priority, rule.Src, rule.Table))
			continue
		}

		res.Trace = append(res.Trace, fmt.Sprintf("rule priority %d: from all lookup %s", rule.Priority, rule.Table))

		best, tied, ok := bestMatch(fib, afi, rule.Table, addr, ifaceHint)
		if !ok {
			res.Trace = append(res.Trace, fmt.Sprintf("table %s: no matching route", rule.Table))
			continue
		}
		if len(tied) > 1 {
			res.Trace = append(res.Trace, fmt.Sprintf("table %s: %d equally-specific candidates, no device hint to disambiguate", rule.Table, len(tied)))
			res.Ambiguous = tied
			return res, nil
		}

		res.Trace = append(res.Trace, fmt.Sprintf("table %s: matched %s via %s", rule.Table, best.Dst, best.Dev))
		matched := best
		res.MatchedRoute = &matched
		matchedRule := rule
		res.MatchedRule = &matchedRule
		res.Reachable = true
		return res, nil
	}

	// Defensive fallback: no policy rule at all was supplied (a caller
	// that only has FIB data, no `ip rule show` output — shouldn't
	// happen for a real node, but a partial/degraded fetch should still
	// answer from "main" rather than reporting unreachable outright).
	if len(sortedRules) == 0 {
		best, tied, ok := bestMatch(fib, afi, "main", addr, ifaceHint)
		if ok && len(tied) <= 1 {
			res.Trace = append(res.Trace, "no policy rules available: falling back to table main")
			matched := best
			res.MatchedRoute = &matched
			res.Reachable = true
			return res, nil
		}
		if len(tied) > 1 {
			res.Ambiguous = tied
			return res, nil
		}
	}

	res.Trace = append(res.Trace, "no route in any evaluated table")
	return res, nil
}

// bestMatch finds the longest-prefix-matching route for addr within
// table, restricted to afi and (when non-empty) ifaceHint. ok is false
// when nothing in the table matches at all. When more than one route ties
// for the longest prefix, best is one of them (deterministic — the
// lowest-metric, then lexicographically-first by Dst) and tied names
// every tied route's device, letting the caller decide whether a tie
// among *different* devices is a genuine ambiguity (Lookup does) or a
// don't-care (a tie between two routes on the *same* device is not
// reported as ambiguous, since it doesn't change which interface traffic
// leaves on).
func bestMatch(fib []FIBRoute, afi AFI, table string, addr netip.Addr, ifaceHint string) (best FIBRoute, tiedDevs []string, ok bool) {
	bestLen := -1
	var candidates []FIBRoute
	for _, r := range fib {
		if r.AFI != afi || r.Table != table {
			continue
		}
		if ifaceHint != "" && r.Dev != ifaceHint {
			continue
		}
		prefix, err := netip.ParsePrefix(r.Dst)
		if err != nil {
			continue
		}
		if !prefix.Contains(addr) {
			continue
		}
		plen := prefix.Bits()
		switch {
		case plen > bestLen:
			bestLen = plen
			candidates = []FIBRoute{r}
		case plen == bestLen:
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return FIBRoute{}, nil, false
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Metric != candidates[j].Metric {
			return candidates[i].Metric < candidates[j].Metric
		}
		return candidates[i].Dst < candidates[j].Dst
	})
	best = candidates[0]

	devs := map[string]bool{}
	var uniqueDevs []string
	for _, c := range candidates {
		if c.Metric != best.Metric {
			continue // only the lowest-metric tier can be a real ambiguity
		}
		if !devs[c.Dev] {
			devs[c.Dev] = true
			uniqueDevs = append(uniqueDevs, c.Dev)
		}
	}
	sort.Strings(uniqueDevs)
	if len(uniqueDevs) > 1 {
		return best, uniqueDevs, true
	}
	return best, uniqueDevs, true
}
