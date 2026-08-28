// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"net/netip"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// matchState is a tri-state rule-match outcome. unknown is the honesty-
// contract lynchpin: a rule the engine cannot decide (an unresolvable
// alias/ipset/macro, or an address test against an unknown IP) is never
// silently treated as matching or not-matching — it forces the verdict to
// Indeterminate.
type matchState int

const (
	matchNo matchState = iota
	matchYes
	matchUnknown
)

// unknownReason accompanies a matchUnknown so the caveat can name what the
// engine could not resolve.
type matchResult struct {
	reason string
	state  matchState
}

// flow is the concrete traffic tuple a rule is tested against.
type flow struct {
	srcIP    netip.Addr
	dstIP    netip.Addr
	proto    string
	port     int
	portSet  bool
	srcKnown bool
	dstKnown bool
}

// fwLookup carries the alias/ipset definitions visible to one guest's rules
// (its own scope plus cluster scope — real pve-firewall's visibility rule).
type fwLookup struct {
	aliases map[string]inventory.FwAlias
	ipsets  map[string]inventory.FwIPSet
}

// lookupFor builds the alias/ipset visibility for a guest enforcement point.
func (e *Engine) lookupFor(guest inventory.Ref) fwLookup {
	lk := fwLookup{aliases: map[string]inventory.FwAlias{}, ipsets: map[string]inventory.FwIPSet{}}
	add := func(rs *inventory.FwRuleset) {
		if rs == nil {
			return
		}
		for _, a := range rs.Aliases {
			lk.aliases[a.Name] = a
		}
		for _, s := range rs.IPSets {
			lk.ipsets[s.Name] = s
		}
	}
	add(e.fw.Cluster)
	add(e.fw.Guests[guest])
	return lk
}

// matchRule tests one leaf rule (never a group-reference marker) against fl.
func matchRule(rule inventory.FwRule, fl flow, lk fwLookup) matchResult {
	// Protocol + port. A macro replaces the rule's own proto/dport with its
	// expansion; an unknown macro is undecidable.
	pp := matchProtoPort(rule, fl)
	if pp.state != matchYes {
		return pp
	}
	// Source / destination address.
	if sm := matchAddr(rule.Source, fl.srcIP, fl.srcKnown, lk, "source"); sm.state != matchYes {
		return sm
	}
	if dm := matchAddr(rule.Dest, fl.dstIP, fl.dstKnown, lk, "dest"); dm.state != matchYes {
		return dm
	}
	return matchResult{state: matchYes}
}

func matchProtoPort(rule inventory.FwRule, fl flow) matchResult {
	if rule.Macro != "" {
		m, ok := fw.MacroExpansion(rule.Macro)
		if !ok {
			return matchResult{state: matchUnknown, reason: "unknown macro " + rule.Macro}
		}
		return matchMacro(m, fl)
	}
	if ps := matchProto(rule.Proto, fl.proto); ps != matchYes {
		return matchResult{state: ps}
	}
	return matchPort(rule.Dport, fl)
}

func matchMacro(m fw.Macro, fl flow) matchResult {
	// A macro matches if the flow matches any of its (proto,dport) pairs.
	sawUnknown := false
	for _, p := range m.Ports {
		if matchProto(p.Proto, fl.proto) == matchNo {
			continue
		}
		pr := matchPort(p.Dport, fl)
		switch pr.state {
		case matchYes:
			return matchResult{state: matchYes}
		case matchUnknown:
			sawUnknown = true
		}
	}
	if sawUnknown {
		return matchResult{state: matchUnknown, reason: "macro " + m.Name + " port match needs a destination port"}
	}
	return matchResult{state: matchNo}
}

// matchProto compares a rule proto against the flow proto. An empty rule
// proto is a wildcard; an unspecified flow proto also wildcards (the caller
// asked a proto-agnostic question).
func matchProto(ruleProto, flowProto string) matchState {
	if ruleProto == "" || flowProto == "" {
		return matchYes
	}
	if strings.EqualFold(ruleProto, flowProto) {
		return matchYes
	}
	return matchNo
}

// matchPort tests a rule/macro dport spec ("", "80", "80,443", "5900:5999")
// against the flow's port. An empty spec is any-port. A non-empty spec with
// an unspecified flow port is undecidable.
func matchPort(spec string, fl flow) matchResult {
	if spec == "" {
		return matchResult{state: matchYes}
	}
	if !fl.portSet {
		return matchResult{state: matchUnknown, reason: "rule restricts destination port " + spec + " but the request specified no port"}
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		lo, hi, ok := parsePortToken(tok)
		if !ok {
			return matchResult{state: matchUnknown, reason: "unparseable port spec " + tok}
		}
		if fl.port >= lo && fl.port <= hi {
			return matchResult{state: matchYes}
		}
	}
	return matchResult{state: matchNo}
}

func parsePortToken(tok string) (lo, hi int, ok bool) {
	if i := strings.IndexAny(tok, ":-"); i >= 0 {
		a, err1 := strconv.Atoi(strings.TrimSpace(tok[:i]))
		b, err2 := strconv.Atoi(strings.TrimSpace(tok[i+1:]))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		if a > b {
			a, b = b, a
		}
		return a, b, true
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, 0, false
	}
	return n, n, true
}

// matchAddr tests a rule source/dest field against an endpoint IP. side is
// "source"/"dest" for the unknown reason string.
func matchAddr(field string, ip netip.Addr, known bool, lk fwLookup, side string) matchResult {
	field = strings.TrimSpace(field)
	if field == "" {
		return matchResult{state: matchYes} // any address
	}
	if !known {
		return matchResult{state: matchUnknown, reason: "rule restricts " + side + " to " + field + " but the endpoint IP is not known"}
	}
	switch {
	case strings.HasPrefix(field, "+"):
		return matchIPSet(field[1:], ip, lk, side)
	case looksLikeCIDR(field):
		pfx, err := netip.ParsePrefix(field)
		if err != nil {
			return matchResult{state: matchUnknown, reason: "unparseable CIDR " + field}
		}
		return yesNo(pfx.Contains(ip))
	case looksLikeIP(field):
		a, err := netip.ParseAddr(field)
		if err != nil {
			return matchResult{state: matchUnknown, reason: "unparseable IP " + field}
		}
		return yesNo(a == ip)
	default:
		// A bare name: an alias, or something the engine does not model
		// (a DNS name, an IP range like "a-b", a dc/... token).
		if a, ok := lk.aliases[field]; ok {
			pfx, err := netip.ParsePrefix(a.CIDR)
			if err != nil {
				if single, e2 := netip.ParseAddr(a.CIDR); e2 == nil {
					return yesNo(single == ip)
				}
				return matchResult{state: matchUnknown, reason: "alias " + field + " has an unparseable value"}
			}
			return yesNo(pfx.Contains(ip))
		}
		return matchResult{state: matchUnknown, reason: side + " reference " + field + " is not a known alias/ipset/IP/CIDR"}
	}
}

// matchIPSet evaluates ipset membership with nomatch (exclusion) entries.
func matchIPSet(name string, ip netip.Addr, lk fwLookup, side string) matchResult {
	set, ok := lk.ipsets[name]
	if !ok {
		return matchResult{state: matchUnknown, reason: side + " ipset +" + name + " is not defined"}
	}
	matched := false
	for _, e := range set.Entries {
		pfx, err := parseCIDROrIP(e.CIDR)
		if err != nil {
			return matchResult{state: matchUnknown, reason: "ipset +" + name + " has an unparseable entry " + e.CIDR}
		}
		if pfx.Contains(ip) {
			if e.NoMatch {
				return matchResult{state: matchNo} // explicit exclusion wins
			}
			matched = true
		}
	}
	return yesNo(matched)
}

func parseCIDROrIP(s string) (netip.Prefix, error) {
	if looksLikeCIDR(s) {
		return netip.ParsePrefix(s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func looksLikeCIDR(s string) bool { return strings.Contains(s, "/") }

func looksLikeIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

func yesNo(b bool) matchResult {
	if b {
		return matchResult{state: matchYes}
	}
	return matchResult{state: matchNo}
}
