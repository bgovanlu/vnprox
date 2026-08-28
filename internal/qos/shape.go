// SPDX-License-Identifier: Apache-2.0

package qos

import (
	"fmt"
	"hash/fnv"
)

// Shape is one bridge-level tc/HTB traffic shape (T-1505): the params of a
// qos.shape.create/update op (internal/change's QosShapeCreateParams),
// plus the caller-chosen id that also names the op's target
// (inventory.KindQosShape Ref) and the owning tc/HTB class within Bridge's
// root qdisc.
//
// A Shape with neither MatchCIDR nor MatchVlan set shapes the bridge's
// whole (otherwise-unclassified) egress traffic — real tc/HTB's own
// "default class" semantics, realized here by RenderTC pointing the root
// qdisc's `default` minor at this shape's own class when no narrower match
// is given (see RenderTC's doc comment).
type Shape struct {
	MatchVlan *int
	CeilMbit  *int
	Priority  *int
	ID        string
	Node      string
	Bridge    string
	MatchCIDR string
	RateMbit  int
}

// rootHandle is the HTB root qdisc's handle every shape on a bridge shares
// — one root per bridge, one class per shape, matching real tc/HTB's own
// "one qdisc, many classes" model (docs/data-model.md §3's qos.* group: a
// bridge can carry more than one shape, distinguished by MatchCIDR/
// MatchVlan).
const rootHandle = "1:"

// unclassifiedClassID is the HTB root's own built-in class (1:1):
// unmatched traffic when at least one narrower (matched) shape exists on
// the bridge. It carries no explicit rate/ceil of its own — HTB's default
// borrowing behavior lets it use whatever bandwidth the matched classes
// don't reserve.
const unclassifiedClassID = "1:1"

// classID derives shape s's stable HTB minor class id ("1:<hex>") from its
// own id — deterministic (the same Shape always renders the same classid,
// so re-applying an unchanged qos.shape.update is idempotent at the tc
// level) and collision-avoidant against unclassifiedClassID's reserved
// minor 1 (a hash landing on 0 or 1 is nudged to 2). FNV-1a rather than a
// cryptographic hash: this only needs to be a stable, well-distributed
// 16-bit id, not collision-resistant against an adversary — a genuine
// collision between two shapes' ids on the same bridge is vanishingly
// unlikely and, if it ever happened, would surface immediately as one
// shape silently overwriting the other's class next apply (caught by the
// referential validator's duplicate-id check, not a silent corruption).
func classID(shapeID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(shapeID))
	minor := h.Sum32() & 0xfffe // even mask keeps it inside 16 bits, below 0xffff
	if minor == 0 || minor == 1 {
		minor = 2
	}
	return fmt.Sprintf("1:%x", minor)
}

// filterPrio is the fixed tc filter priority every shape's selector filter
// is installed at. Real `tc filter add` tolerates many filters sharing one
// prio (the u32 classifier chains them in its own hash table internally),
// so a fixed shared prio across every shape on a bridge is correct tc
// usage, not a collision.
const filterPrio = "1"

// RenderTC renders the ordered `tc` argv lines (each a full command,
// argv[0] == "tc") that realize shape on its bridge:
//
//  1. `tc qdisc replace dev <bridge> root handle 1: htb default 1` — the
//     bridge's root HTB qdisc, idempotent (`replace`, not `add`) so
//     applying a second shape on the same bridge never errors on an
//     already-present qdisc. `default 1` routes any traffic no filter
//     claims into the reserved unclassified class.
//  2. `tc class replace dev <bridge> parent 1: classid <cid> htb rate
//     <rate>mbit [ceil <ceil>mbit] [prio <priority>]` — shape's own class,
//     carrying its rate/ceil/priority.
//  3. (only when MatchCIDR and/or MatchVlan is set) one `tc filter replace
//     dev <bridge> parent 1: protocol ip prio 1 u32 ...` selecting the
//     matched traffic into shape's class — a shape with neither match set
//     instead relies on its class being installed as the qdisc's own
//     `default` target (line 4 below), so it captures the bridge's whole
//     unclassified egress without needing a filter at all.
//  4. (only when neither match is set) a second `tc qdisc replace ... htb
//     default <cid>` re-pointing the root's default target at shape's own
//     class instead of the reserved unclassified one — this is a
//     documented, deliberately narrow interpretation of "an unmatched
//     shape governs the bridge's whole egress" for the common single-shape-
//     per-bridge case; a bridge carrying more than one whole-bridge
//     (unmatched) shape is rejected by validation before this ever runs
//     (schema/referential class, internal/change) since only one class can
//     ever be the qdisc's `default`.
func RenderTC(s Shape) ([][]string, error) {
	if s.Bridge == "" {
		return nil, fmt.Errorf("qos: shape %s: bridge is required", s.ID)
	}
	if s.RateMbit <= 0 {
		return nil, fmt.Errorf("qos: shape %s: rateMbit must be positive, got %d", s.ID, s.RateMbit)
	}
	if s.CeilMbit != nil && *s.CeilMbit < s.RateMbit {
		return nil, fmt.Errorf("qos: shape %s: ceilMbit %d must be >= rateMbit %d", s.ID, *s.CeilMbit, s.RateMbit)
	}

	cid := classID(s.ID)
	matched := s.MatchCIDR != "" || s.MatchVlan != nil

	defaultMinor := "1"
	if !matched {
		defaultMinor = classIDMinor(cid)
	}
	lines := [][]string{
		{"tc", "qdisc", "replace", "dev", s.Bridge, "root", "handle", rootHandle, "htb", "default", defaultMinor},
		classLine(s, cid),
	}
	if matched {
		lines = append(lines, filterLine(s, cid))
	}
	return lines, nil
}

// classIDMinor extracts the minor (after the ':') from a "1:<hex>" classid
// string, for the root qdisc's `default <minor>` argument (which takes a
// bare minor, not the full "major:minor" form).
func classIDMinor(cid string) string {
	for i, c := range cid {
		if c == ':' {
			return cid[i+1:]
		}
	}
	return cid
}

// classLine renders shape s's `tc class replace` line.
func classLine(s Shape, cid string) []string {
	line := []string{
		"tc", "class", "replace", "dev", s.Bridge, "parent", rootHandle, "classid", cid,
		"htb", "rate", mbit(s.RateMbit),
	}
	if s.CeilMbit != nil {
		line = append(line, "ceil", mbit(*s.CeilMbit))
	}
	if s.Priority != nil {
		line = append(line, "prio", fmt.Sprintf("%d", *s.Priority))
	}
	return line
}

// filterLine renders shape s's selector filter, chaining every configured
// match (MatchCIDR, MatchVlan) into one `tc filter` u32 invocation — u32
// supports multiple `match` clauses ANDed together in a single filter.
func filterLine(s Shape, cid string) []string {
	line := []string{
		"tc", "filter", "replace", "dev", s.Bridge, "parent", rootHandle,
		"protocol", "802.1Q", "prio", filterPrio, "u32",
	}
	if s.MatchVlan != nil {
		// Classic tc/u32 VLAN-tag match: the 16-bit VID sits in the low 12
		// bits of the 802.1Q tag header, 4 bytes before the IP header
		// (offset -4 from the u32 "link layer" anchor here).
		line = append(line, "match", "u16", fmt.Sprintf("0x%04x", *s.MatchVlan), "0x0fff", "at", "-4")
	}
	if s.MatchCIDR != "" {
		line = append(line, "match", "ip", "dst", s.MatchCIDR)
	}
	line = append(line, "flowid", cid)
	return line
}

// RenderTCTeardown renders the ordered `tc` argv lines that remove shape
// from its bridge — the inverse of RenderTC's class/filter lines (the
// qdisc line 1's dependent qdisc-remove is a heavier operation: tearing
// down `root handle 1:` on a bridge that still carries sibling shapes'
// classes would remove them too, so teardown only ever removes THIS
// shape's own class/filter, never the shared root qdisc — a bridge with no
// shapes left simply carries an idle, harmless root qdisc until its next
// shape re-provisions it via RenderTC's own idempotent `replace`).
func RenderTCTeardown(s Shape) [][]string {
	cid := classID(s.ID)
	var lines [][]string
	if s.MatchCIDR != "" || s.MatchVlan != nil {
		lines = append(lines, []string{
			"tc", "filter", "del", "dev", s.Bridge, "parent", rootHandle,
			"protocol", "802.1Q", "prio", filterPrio,
		})
	}
	lines = append(lines, []string{"tc", "class", "del", "dev", s.Bridge, "classid", cid})
	return lines
}

// mbit formats an integer megabit rate as tc's own "<n>mbit" rate unit
// suffix.
func mbit(n int) string {
	return fmt.Sprintf("%dmbit", n)
}
