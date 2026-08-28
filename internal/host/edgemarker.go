// SPDX-License-Identifier: Apache-2.0

// edgemarker.go backs T-1403's Edge & NAT cockpit: the encode/decode pair
// that lets internal/change/ifaces (the mutator) and internal/edge (the
// read view) agree on exactly one on-disk representation for a nat.*/
// route.static.* op's state, with no second, shadow store anywhere. Per
// this op family's own doc comment (internal/change/ifaces/edgeop.go): a
// nat.masquerade/nat.portforward/route.static rule's *only* record is the
// post-up/post-down stanza pair it renders into /etc/network/interfaces —
// each generated line carries a trailing shell comment (interfaces(5) exec's
// post-up/post-down value via `/bin/sh -c`, where a a trailing "# ..." is an
// ordinary, inert shell comment) encoding that rule's full field set, so a
// later read (GET /edge/nat, GET /edge/routes) or a later update/delete op
// can recover the exact rule without needing to parse the iptables/ip
// command text itself back apart. This mirrors the "vnprox writes the file
// it will re-read" pattern iface.raw.replace already established
// (docs/features/change-management.md §7) — the interfaces file is both the
// only write target and the only read source, never duplicated into SQLite.

package host

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Marker prefixes: one per rule kind, each followed by a url.Values-encoded
// field set (net/url's own percent-encoding handles arbitrary Comment text
// safely — commas, spaces, '=' — without a hand-rolled escaping scheme).
const (
	EdgeMarkerNatMasquerade  = "vnprox-edge:nat-masquerade:"
	EdgeMarkerNatPortForward = "vnprox-edge:nat-portforward:"
	EdgeMarkerStaticRoute    = "vnprox-edge:route-static:"

	// edgeMarkerSentinel is the common prefix every marker of any kind
	// starts with — CutEdgeMarker's search string.
	edgeMarkerSentinel = "vnprox-edge:"
)

// CutEdgeMarker locates a post-up/post-down BodyOption Value's trailing
// "# vnprox-edge:..." marker comment and returns the marker text itself
// (without the leading "# "), so a caller can then try each kind's own
// Decode*Marker function against it (internal/change/ifaces and
// internal/edge both do this — one shared helper, not two copies). ok is
// false when value carries no such marker at all (a hand-written or
// pre-existing post-up/post-down line vnprox itself never generated).
func CutEdgeMarker(value string) (string, bool) {
	i := strings.Index(value, "# "+edgeMarkerSentinel)
	if i < 0 {
		return "", false
	}
	return value[i+2:], true
}

// NatMasqueradeConfig is one nat.masquerade.create rule's full state,
// round-tripped through its generated lines' trailing marker comment.
type NatMasqueradeConfig struct {
	ID         string
	Iface      string
	SourceCIDR string
	Comment    string
}

// EncodeNatMasqueradeMarker renders c as a trailing marker comment (without
// the leading "# " — callers append that, see NatMasqueradeCommands).
func EncodeNatMasqueradeMarker(c NatMasqueradeConfig) string {
	v := url.Values{}
	v.Set("id", c.ID)
	v.Set("iface", c.Iface)
	v.Set("sourceCidr", c.SourceCIDR)
	if c.Comment != "" {
		v.Set("comment", c.Comment)
	}
	return EdgeMarkerNatMasquerade + v.Encode()
}

// DecodeNatMasqueradeMarker parses a trailing marker comment (the "vnprox-
// edge:nat-masquerade:..." suffix of a post-up/post-down Value, without the
// leading "# ") back into a NatMasqueradeConfig. ok is false when s does not
// carry this marker's prefix at all, or its query-encoded suffix fails to
// parse (defensive — a hand-edited or corrupted line is simply not
// recognized, never a hard parse error propagated to the caller).
func DecodeNatMasqueradeMarker(s string) (NatMasqueradeConfig, bool) {
	rest, ok := strings.CutPrefix(s, EdgeMarkerNatMasquerade)
	if !ok {
		return NatMasqueradeConfig{}, false
	}
	v, err := url.ParseQuery(rest)
	if err != nil {
		return NatMasqueradeConfig{}, false
	}
	return NatMasqueradeConfig{ID: v.Get("id"), Iface: v.Get("iface"), SourceCIDR: v.Get("sourceCidr"), Comment: v.Get("comment")}, true
}

// NatMasqueradeCommands renders c's post-up and post-down shell command
// lines (without leading "post-up "/"post-down " — MutateNat* prepends
// those as the BodyOption key), each with the encoded marker appended as a
// trailing shell comment.
func NatMasqueradeCommands(c NatMasqueradeConfig) (up, down string) {
	marker := "# " + EncodeNatMasqueradeMarker(c)
	up = "iptables -t nat -A POSTROUTING -s " + c.SourceCIDR + " -o " + c.Iface + " -j MASQUERADE " + marker
	down = "iptables -t nat -D POSTROUTING -s " + c.SourceCIDR + " -o " + c.Iface + " -j MASQUERADE " + marker
	return up, down
}

// NatPortForwardConfig is one nat.portforward.* rule's full state.
type NatPortForwardConfig struct {
	ID      string
	Iface   string
	Proto   string // tcp|udp
	IntIP   string
	Comment string
	ExtPort int
	IntPort int
}

func EncodeNatPortForwardMarker(c NatPortForwardConfig) string {
	v := url.Values{}
	v.Set("id", c.ID)
	v.Set("iface", c.Iface)
	v.Set("proto", c.Proto)
	v.Set("extPort", strconv.Itoa(c.ExtPort))
	v.Set("intIp", c.IntIP)
	v.Set("intPort", strconv.Itoa(c.IntPort))
	if c.Comment != "" {
		v.Set("comment", c.Comment)
	}
	return EdgeMarkerNatPortForward + v.Encode()
}

func DecodeNatPortForwardMarker(s string) (NatPortForwardConfig, bool) {
	rest, ok := strings.CutPrefix(s, EdgeMarkerNatPortForward)
	if !ok {
		return NatPortForwardConfig{}, false
	}
	v, err := url.ParseQuery(rest)
	if err != nil {
		return NatPortForwardConfig{}, false
	}
	extPort, _ := strconv.Atoi(v.Get("extPort"))
	intPort, _ := strconv.Atoi(v.Get("intPort"))
	return NatPortForwardConfig{
		ID: v.Get("id"), Iface: v.Get("iface"), Proto: v.Get("proto"),
		ExtPort: extPort, IntIP: v.Get("intIp"), IntPort: intPort, Comment: v.Get("comment"),
	}, true
}

// NatPortForwardCommands renders c's post-up/post-down DNAT command lines.
func NatPortForwardCommands(c NatPortForwardConfig) (up, down string) {
	marker := "# " + EncodeNatPortForwardMarker(c)
	dest := c.IntIP + ":" + strconv.Itoa(c.IntPort)
	tail := " PREROUTING -i " + c.Iface + " -p " + c.Proto +
		" --dport " + strconv.Itoa(c.ExtPort) + " -j DNAT --to-destination " + dest + " " + marker
	up = fmt.Sprintf("iptables -t nat -A%s", tail)
	down = fmt.Sprintf("iptables -t nat -D%s", tail)
	return up, down
}

// StaticRouteConfig is one route.static.* rule's full state.
type StaticRouteConfig struct {
	ID       string
	Iface    string
	DestCIDR string
	Gateway  string
	Comment  string
	Metric   int
}

func EncodeStaticRouteMarker(c StaticRouteConfig) string {
	v := url.Values{}
	v.Set("id", c.ID)
	v.Set("iface", c.Iface)
	v.Set("destCidr", c.DestCIDR)
	v.Set("gateway", c.Gateway)
	if c.Metric != 0 {
		v.Set("metric", strconv.Itoa(c.Metric))
	}
	if c.Comment != "" {
		v.Set("comment", c.Comment)
	}
	return EdgeMarkerStaticRoute + v.Encode()
}

func DecodeStaticRouteMarker(s string) (StaticRouteConfig, bool) {
	rest, ok := strings.CutPrefix(s, EdgeMarkerStaticRoute)
	if !ok {
		return StaticRouteConfig{}, false
	}
	v, err := url.ParseQuery(rest)
	if err != nil {
		return StaticRouteConfig{}, false
	}
	metric, _ := strconv.Atoi(v.Get("metric"))
	return StaticRouteConfig{
		ID: v.Get("id"), Iface: v.Get("iface"), DestCIDR: v.Get("destCidr"),
		Gateway: v.Get("gateway"), Metric: metric, Comment: v.Get("comment"),
	}, true
}

// StaticRouteCommands renders c's post-up/post-down `ip route` command
// lines.
func StaticRouteCommands(c StaticRouteConfig) (up, down string) {
	marker := "# " + EncodeStaticRouteMarker(c)
	tail := c.DestCIDR + " via " + c.Gateway + " dev " + c.Iface
	if c.Metric != 0 {
		tail += " metric " + strconv.Itoa(c.Metric)
	}
	up = "ip route add " + tail + " " + marker
	down = "ip route del " + tail + " " + marker
	return up, down
}
