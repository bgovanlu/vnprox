// SPDX-License-Identifier: Apache-2.0

// edgeop.go implements T-1403's nat.masquerade.*/nat.portforward.*/
// route.static.* mutators: each op appends (create), replaces (update), or
// removes (delete) a post-up/post-down stanza pair inside an *existing*
// iface stanza — the same "vnprox writes the file it will re-read" pattern
// iface.raw.replace already established (docs/features/change-management.md
// §7), never a second mutation path or a shadow store. A rule's full state
// lives *only* in its generated lines' own trailing marker comment
// (host.EncodeNat*Marker/EncodeStaticRouteMarker) — there is no other
// record of it anywhere in vnprox, so update/delete recover it by scanning
// the file for that marker rather than trusting the op's own (possibly
// stale, possibly Iface-migrating) params.

package ifaces

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// edgeRuleMatcher extracts the rule id a marked BodyOption's Value encodes,
// for one specific rule kind (ok is false for a line that isn't that kind's
// marker at all, including another kind's).
type edgeRuleMatcher func(value string) (id string, ok bool)

func natMasqueradeMatcher(value string) (string, bool) {
	s, ok := host.CutEdgeMarker(value)
	if !ok {
		return "", false
	}
	c, ok := host.DecodeNatMasqueradeMarker(s)
	return c.ID, ok
}

func natPortForwardMatcher(value string) (string, bool) {
	s, ok := host.CutEdgeMarker(value)
	if !ok {
		return "", false
	}
	c, ok := host.DecodeNatPortForwardMarker(s)
	return c.ID, ok
}

func staticRouteMatcher(value string) (string, bool) {
	s, ok := host.CutEdgeMarker(value)
	if !ok {
		return "", false
	}
	c, ok := host.DecodeStaticRouteMarker(s)
	return c.ID, ok
}

// removeEdgeRuleLines removes every post-up/post-down BodyOption line
// anywhere in f (any iface stanza, not just one named Iface) whose marker
// decodes — via match — to id, returning how many lines were removed.
// Scanning every stanza (rather than trusting the op's own Iface field) is
// what makes an update that changes Iface, or a delete whose caller no
// longer remembers which stanza the rule lives in, still correct: the
// lines are found wherever they actually are.
func removeEdgeRuleLines(f *host.File, id string, match edgeRuleMatcher) int {
	removed := 0
	for _, e := range f.Ifaces() {
		out := make([]host.BodyItem, 0, len(e.Body))
		for _, item := range e.Body {
			if item.Kind == host.BodyOption && (item.Key == "post-up" || item.Key == "post-down") {
				if rid, ok := match(item.Value); ok && rid == id {
					removed++
					continue
				}
			}
			out = append(out, item)
		}
		e.Body = out
	}
	return removed
}

func findNatPortForwardConfig(f *host.File, id string) (host.NatPortForwardConfig, bool) {
	for _, e := range f.Ifaces() {
		for _, item := range e.Body {
			if item.Kind != host.BodyOption || item.Key != "post-up" {
				continue
			}
			s, ok := host.CutEdgeMarker(item.Value)
			if !ok {
				continue
			}
			if c, ok := host.DecodeNatPortForwardMarker(s); ok && c.ID == id {
				return c, true
			}
		}
	}
	return host.NatPortForwardConfig{}, false
}

func findStaticRouteConfig(f *host.File, id string) (host.StaticRouteConfig, bool) {
	for _, e := range f.Ifaces() {
		for _, item := range e.Body {
			if item.Kind != host.BodyOption || item.Key != "post-up" {
				continue
			}
			s, ok := host.CutEdgeMarker(item.Value)
			if !ok {
				continue
			}
			if c, ok := host.DecodeStaticRouteMarker(s); ok && c.ID == id {
				return c, true
			}
		}
	}
	return host.StaticRouteConfig{}, false
}

// appendRulePair inserts a post-up/post-down BodyOption pair (up/down
// already carrying their trailing marker) into ifaceName's stanza, just
// before any trailing run of blank body lines (entryutil.go's
// insertBeforeTrailingBlanks — the same placement every other append-style
// mutator in this package uses).
func appendRulePair(f *host.File, ifaceName, up, down, nl string) error {
	e, ok := f.Iface(ifaceName)
	if !ok {
		return fmt.Errorf("ifaces: edge rule: iface %q: %w", ifaceName, ErrNotFound)
	}
	items := []host.BodyItem{optionItem("post-up", up, nl), optionItem("post-down", down, nl)}
	e.Body = insertBeforeTrailingBlanks(e.Body, items, nl)
	return nil
}

// --- nat.masquerade.* -------------------------------------------------------

func mutateNatMasqueradeCreate(f *host.File, o NatMasqueradeCreate, nl string) error {
	cfg := host.NatMasqueradeConfig{ID: o.Target.ID, Iface: o.Iface, SourceCIDR: o.SourceCIDR, Comment: o.Comment}
	up, down := host.NatMasqueradeCommands(cfg)
	return appendRulePair(f, o.Iface, up, down, nl)
}

func mutateNatMasqueradeDelete(f *host.File, o NatMasqueradeDelete) error {
	if n := removeEdgeRuleLines(f, o.Target.ID, natMasqueradeMatcher); n == 0 {
		return fmt.Errorf("ifaces: nat.masquerade.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	return nil
}

// --- nat.portforward.* -------------------------------------------------------

func mutateNatPortForwardCreate(f *host.File, o NatPortForwardCreate, nl string) error {
	cfg := host.NatPortForwardConfig{
		ID: o.Target.ID, Iface: o.Iface, Proto: o.Proto, ExtPort: o.ExtPort,
		IntIP: o.IntIP, IntPort: o.IntPort, Comment: o.Comment,
	}
	up, down := host.NatPortForwardCommands(cfg)
	return appendRulePair(f, o.Iface, up, down, nl)
}

func mutateNatPortForwardUpdate(f *host.File, o NatPortForwardUpdate, nl string) error {
	cur, ok := findNatPortForwardConfig(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: nat.portforward.update %q: %w", o.Target.ID, ErrNotFound)
	}
	if o.Iface != nil {
		cur.Iface = *o.Iface
	}
	if o.Proto != nil {
		cur.Proto = *o.Proto
	}
	if o.IntIP != nil {
		cur.IntIP = *o.IntIP
	}
	if o.Comment != nil {
		cur.Comment = *o.Comment
	}
	if o.ExtPort != nil {
		cur.ExtPort = *o.ExtPort
	}
	if o.IntPort != nil {
		cur.IntPort = *o.IntPort
	}
	removeEdgeRuleLines(f, o.Target.ID, natPortForwardMatcher)
	up, down := host.NatPortForwardCommands(cur)
	return appendRulePair(f, cur.Iface, up, down, nl)
}

func mutateNatPortForwardDelete(f *host.File, o NatPortForwardDelete) error {
	if n := removeEdgeRuleLines(f, o.Target.ID, natPortForwardMatcher); n == 0 {
		return fmt.Errorf("ifaces: nat.portforward.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	return nil
}

// --- route.static.* -------------------------------------------------------

func mutateRouteStaticCreate(f *host.File, o RouteStaticCreate, nl string) error {
	cfg := host.StaticRouteConfig{
		ID: o.Target.ID, Iface: o.Iface, DestCIDR: o.DestCIDR, Gateway: o.Gateway,
		Metric: o.Metric, Comment: o.Comment,
	}
	up, down := host.StaticRouteCommands(cfg)
	return appendRulePair(f, o.Iface, up, down, nl)
}

func mutateRouteStaticUpdate(f *host.File, o RouteStaticUpdate, nl string) error {
	cur, ok := findStaticRouteConfig(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: route.static.update %q: %w", o.Target.ID, ErrNotFound)
	}
	if o.Iface != nil {
		cur.Iface = *o.Iface
	}
	if o.DestCIDR != nil {
		cur.DestCIDR = *o.DestCIDR
	}
	if o.Gateway != nil {
		cur.Gateway = *o.Gateway
	}
	if o.Comment != nil {
		cur.Comment = *o.Comment
	}
	if o.Metric != nil {
		cur.Metric = *o.Metric
	}
	removeEdgeRuleLines(f, o.Target.ID, staticRouteMatcher)
	up, down := host.StaticRouteCommands(cur)
	return appendRulePair(f, cur.Iface, up, down, nl)
}

func mutateRouteStaticDelete(f *host.File, o RouteStaticDelete) error {
	if n := removeEdgeRuleLines(f, o.Target.ID, staticRouteMatcher); n == 0 {
		return fmt.Errorf("ifaces: route.static.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	return nil
}
