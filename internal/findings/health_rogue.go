// health_rogue.go implements T-1605's rogue-service / L2-anomaly detection
// producer (source "rogue"): rogue DHCP servers, unexpected IPv6 RAs,
// ARP/ND spoofing, and unknown MACs on operator-flagged protected segments.
//
// Every signal here is computed entirely from data the collectors already
// gather — T-805's ARP/IPv6-neighbor observations, T-1404's IPv6 RA feed,
// the existing DHCP lease/reservation views, and the inventory graph's own
// MAC knowledge — never a new probe path. All four checks are raised as
// error-severity findings, are never fixable, and are hysteresis-exempt: a
// spoofed/rogue/unknown-MAC signal is a security event, not a noisy counter
// to debounce (docs/features/ipam.md §1, the T-1605 card's own contract).
// This producer is detection-only — it introduces zero changeset ops and
// zero write routes; blocking a suspected rogue stays a human-confirmed fix,
// exactly like every other detection-only producer in the unified stream.
//
// The one piece of cross-cycle state is arp_spoof_suspected's churn window
// (an IP flapping between MACs over a short trailing window), tracked here in
// an arpChurnTracker mirroring stpBurstTracker's own precedent (health_stpburst.go)
// rather than in a separate package — the same "stateful health check keeps
// its tracker in the findings package" convention every debounced check follows.

package findings

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// The four rogue-detection check names (docs/api.md's GET /findings `check`
// vocabulary for source "rogue").
const (
	CheckRogueDHCPServer            = "rogue_dhcp_server"
	CheckUnexpectedRA               = "unexpected_ra"
	CheckArpSpoofSuspected          = "arp_spoof_suspected"
	CheckUnknownMacProtectedSegment = "unknown_mac_protected_segment"
)

const rogueDocsLink = "docs/features/ipam.md#1-data-sources"

// arpChurnWindow / arpChurnThreshold define arp_spoof_suspected's detection:
// an IP whose resolved MAC has changed arpChurnThreshold or more times within
// the trailing window is reported as suspected spoofing. A single MAC/IP
// reassignment — a DHCP lease handed to a new host, then stable — produces
// exactly one change and never fires; only rapid oscillation between MACs
// (a spoofer answering ARP alongside the real host) crosses the threshold.
// This is the churn signal itself, not a debounce of an already-computed
// verdict, so it does not violate the "hysteresis-exempt" contract.
const (
	arpChurnWindow    = 2 * time.Minute
	arpChurnThreshold = 3
)

// DHCPServerObservation is one observed DHCP-offering source on a
// DHCP-enabled SDN subnet, from raw lease-file / DHCP-traffic observation —
// the seam a production collector feeds (a fixture in tests). MAC/IP identify
// the offering server; Iface/Node/SubnetCIDR locate where the offer was seen.
type DHCPServerObservation struct {
	MAC        string
	IP         string
	Iface      string
	Node       string
	SubnetCIDR string
}

// RAObservation is one observed IPv6 Router Advertisement source (T-1404's
// RA visibility feed). An empty feed is the documented pre-T-1404 state:
// unexpected_ra is a real no-op until that feed actually ships RA sources.
type RAObservation struct {
	SourceMAC  string
	SourceIP   string
	Iface      string
	Node       string
	SegmentRef string
}

// NeighborObservation is one T-805 neighbor-table entry (an IP resolved to a
// MAC on a node), the raw churn signal arp_spoof_suspected consumes across
// cycles.
type NeighborObservation struct {
	IP   string
	MAC  string
	Node string
}

// RogueScan bundles every observation feed the three feed-driven rogue
// checks consume, plus the config-truth sets they compare against. It is
// recomputed fresh each findings cycle by the RogueProvider adapter (never
// persisted), exactly like every other live-state producer seam. The
// fourth check (unknown_mac_protected_segment) is graph+config-derived and
// needs no feed here — it reads the inventory snapshot and the
// protected-segments config directly.
type RogueScan struct {
	// DHCPOffers are the observed DHCP-offering sources this cycle.
	DHCPOffers []DHCPServerObservation
	// LegitDHCPServerMACs are the MACs of the subnets' own configured PVE-SDN
	// DHCP range owners (GET /sdn/dhcp's config-truth view). An offer from any
	// MAC outside this set is a rogue DHCP server.
	LegitDHCPServerMACs []string
	// RAs are the observed IPv6 RA sources this cycle (T-1404 feed).
	RAs []RAObservation
	// LegitRASourceMACs are the known PVE-configured RA source MACs; an RA
	// from any MAC outside this set is unexpected.
	LegitRASourceMACs []string
	// Neighbors is this cycle's cluster-wide neighbor-table snapshot (T-805).
	Neighbors []NeighborObservation
}

// RogueProvider is the findings engine's seam onto the rogue-detection
// observation feeds (cmd/vnproxd's rogueScanAdapter builds a RogueScan from
// internal/neighbor, the DHCP config view, and — once it lands — T-1404's RA
// feed). A nil provider skips the three feed-driven checks entirely, the same
// nil-safe degradation every other optional producer seam uses;
// unknown_mac_protected_segment does not depend on this seam and still runs
// from the graph + config alone.
type RogueProvider interface {
	RogueScan() RogueScan
}

// arpIPState is one IP's recent resolved-MAC history within the churn window.
type arpIPState struct {
	lastMAC     string
	changeTimes []time.Time
	seen        bool
}

// arpChurnTracker holds every observed IP's arpIPState across Engine cycles.
// Safe for concurrent use, mirroring stpBurstTracker.
type arpChurnTracker struct {
	state map[string]*arpIPState
	mu    sync.Mutex
}

func newArpChurnTracker() *arpChurnTracker {
	return &arpChurnTracker{state: map[string]*arpIPState{}}
}

// observe records ip's currently-resolved mac at now, appending a change
// event iff it differs from the last-observed MAC (and a MAC has already been
// observed once — the first observation never counts, so a freshly-learned
// neighbor never fires on its own), then returns how many change events
// remain within the trailing window.
func (t *arpChurnTracker) observe(ip, mac string, now time.Time, window time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.state[ip]
	if st == nil {
		st = &arpIPState{}
		t.state[ip] = st
	}
	if st.seen && st.lastMAC != mac {
		st.changeTimes = append(st.changeTimes, now)
	}
	st.seen = true
	st.lastMAC = mac

	cutoff := now.Add(-window)
	kept := st.changeTimes[:0]
	for _, ct := range st.changeTimes {
		if ct.After(cutoff) {
			kept = append(kept, ct)
		}
	}
	st.changeTimes = kept
	return len(st.changeTimes)
}

func (t *arpChurnTracker) prune(live map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.state {
		if !live[k] {
			delete(t.state, k)
		}
	}
}

// rogueFindings runs the three feed-driven rogue checks (nil-safe on p) and
// the graph+config-derived unknown_mac_protected_segment check, returning the
// combined, deterministically-ordered slice.
func rogueFindings(p RogueProvider, snap inventory.Snapshot, protectedSegments []string, tracker *arpChurnTracker, now time.Time) []Finding {
	var out []Finding
	out = append(out, checkUnknownMacProtectedSegment(snap, protectedSegments)...)
	if p != nil {
		scan := p.RogueScan()
		out = append(out, checkRogueDHCPServer(scan)...)
		out = append(out, checkUnexpectedRA(scan)...)
		out = append(out, checkArpSpoofSuspected(scan.Neighbors, tracker, now)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// checkRogueDHCPServer flags every observed DHCP offer whose source MAC is not
// one of the subnet's own configured PVE-SDN DHCP range owners. Fires once per
// distinct (MAC, subnet); refs names the offending MAC and interface, never
// the whole subnet.
func checkRogueDHCPServer(scan RogueScan) []Finding {
	legit := macSet(scan.LegitDHCPServerMACs)
	seen := map[string]bool{}
	var out []Finding
	for _, off := range scan.DHCPOffers {
		mac := normalizeMAC(off.MAC)
		if mac == "" || legit[mac] {
			continue
		}
		key := mac + "|" + off.SubnetCIDR
		if seen[key] {
			continue
		}
		seen[key] = true
		detail := fmt.Sprintf("a DHCP server at MAC %s (%s) is offering leases on subnet %s via %s, but it is not the subnet's configured PVE-SDN DHCP range owner — a rogue DHCP server can hand clients a poisoned gateway/DNS",
			off.MAC, firstNonEmpty(off.IP, "unknown IP"), firstNonEmpty(off.SubnetCIDR, "an unknown subnet"), firstNonEmpty(off.Iface, "an unknown interface"))
		refs := []string{off.MAC}
		if off.Iface != "" {
			refs = append(refs, off.Iface)
		}
		out = append(out, newRogueFinding(CheckRogueDHCPServer, detail, off.Node, refs, key))
	}
	return out
}

// checkUnexpectedRA flags every observed IPv6 RA whose source MAC is not a
// known PVE-configured RA source. An empty RA feed (pre-T-1404) yields no
// findings — the documented no-op — because the loop simply has nothing to
// range over.
func checkUnexpectedRA(scan RogueScan) []Finding {
	legit := macSet(scan.LegitRASourceMACs)
	seen := map[string]bool{}
	var out []Finding
	for _, ra := range scan.RAs {
		mac := normalizeMAC(ra.SourceMAC)
		if mac == "" || legit[mac] {
			continue
		}
		key := mac + "|" + ra.SegmentRef
		if seen[key] {
			continue
		}
		seen[key] = true
		detail := fmt.Sprintf("an unexpected IPv6 Router Advertisement is originating from MAC %s (%s) on %s%s, but it is not a known PVE-configured RA source — a rogue RA can redirect IPv6 hosts to an attacker's gateway",
			ra.SourceMAC, firstNonEmpty(ra.SourceIP, "unknown source"), firstNonEmpty(ra.Iface, "an unknown interface"), segmentSuffix(ra.SegmentRef))
		refs := []string{ra.SourceMAC}
		if ra.SegmentRef != "" {
			refs = append(refs, ra.SegmentRef)
		}
		out = append(out, newRogueFinding(CheckUnexpectedRA, detail, ra.Node, refs, key))
	}
	return out
}

// checkArpSpoofSuspected flags every IP whose resolved MAC has churned across
// arpChurnThreshold or more distinct values within the trailing window. A
// single DHCP-renewal MAC/IP reassignment (one change, then stability) never
// crosses the threshold; only rapid oscillation does — the distinguishing
// property AC3 asserts.
func checkArpSpoofSuspected(neighbors []NeighborObservation, tracker *arpChurnTracker, now time.Time) []Finding {
	// Deterministic processing order so within-cycle conflicts (the same IP
	// resolving to two MACs in one scan) count reproducibly.
	obs := append([]NeighborObservation(nil), neighbors...)
	sort.Slice(obs, func(i, j int) bool {
		if obs[i].IP != obs[j].IP {
			return obs[i].IP < obs[j].IP
		}
		if normalizeMAC(obs[i].MAC) != normalizeMAC(obs[j].MAC) {
			return normalizeMAC(obs[i].MAC) < normalizeMAC(obs[j].MAC)
		}
		return obs[i].Node < obs[j].Node
	})

	nodesByIP := map[string]map[string]bool{}
	macsByIP := map[string]map[string]bool{}
	live := map[string]bool{}
	counts := map[string]int{}
	for _, n := range obs {
		mac := normalizeMAC(n.MAC)
		if n.IP == "" || mac == "" {
			continue
		}
		live[n.IP] = true
		counts[n.IP] = tracker.observe(n.IP, mac, now, arpChurnWindow)
		if nodesByIP[n.IP] == nil {
			nodesByIP[n.IP] = map[string]bool{}
			macsByIP[n.IP] = map[string]bool{}
		}
		if n.Node != "" {
			nodesByIP[n.IP][n.Node] = true
		}
		macsByIP[n.IP][n.MAC] = true
	}
	tracker.prune(live)

	var out []Finding
	for ip, count := range counts {
		if count < arpChurnThreshold {
			continue
		}
		macs := setKeys(macsByIP[ip])
		nodes := setKeys(nodesByIP[ip])
		detail := fmt.Sprintf("IP %s has resolved to %d different MACs (%s) in the last %s — %d changes, well above normal DHCP-renewal reassignment; suspected ARP/ND spoofing",
			ip, len(macs), strings.Join(macs, ", "), arpChurnWindow.Round(time.Second), count)
		refs := append([]string{ip}, macs...)
		out = append(out, newRogueFinding(CheckArpSpoofSuspected, detail, strings.Join(nodes, ","), refs, ip))
	}
	return out
}

// checkUnknownMacProtectedSegment flags every MAC learned on a bridge listed
// in protectedSegments that matches no known guest/PhysNic/LLDP-neighbor MAC
// in the inventory graph. A segment not listed in protectedSegments never
// fires this check, regardless of unknown MACs on it; an empty config list
// disables the check entirely.
func checkUnknownMacProtectedSegment(snap inventory.Snapshot, protectedSegments []string) []Finding {
	protected := map[string]bool{}
	for _, s := range protectedSegments {
		if s = strings.TrimSpace(s); s != "" {
			protected[s] = true
		}
	}
	if len(protected) == 0 {
		return nil
	}
	known := knownMACs(snap)

	var out []Finding
	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok || !protected[br.Name] {
			continue
		}
		bridgeRef := br.GetRef().String()
		node := br.GetRef().Node
		for _, fdb := range br.FDB {
			mac := normalizeMAC(fdb.Mac)
			// Master entries are the bridge's own address, not a joined host.
			if mac == "" || fdb.Master || known[mac] {
				continue
			}
			detail := fmt.Sprintf("MAC %s has joined protected segment %s on node %s but matches no known guest, physical NIC, or LLDP-neighbor in the inventory — an unrecognized device on a segment you flagged protected",
				fdb.Mac, br.Name, node)
			out = append(out, newRogueFinding(CheckUnknownMacProtectedSegment, detail, node, []string{bridgeRef, fdb.Mac}, br.Name+"|"+mac))
		}
	}
	return out
}

// knownMACs collects every MAC the inventory graph already accounts for: a
// guest NIC's MAC, a physical NIC's MAC, and an LLDP neighbor's chassis/port
// ID when it is a MAC address. Keys are normalized (lower-cased, trimmed).
func knownMACs(snap inventory.Snapshot) map[string]bool {
	out := map[string]bool{}
	add := func(mac string) {
		if m := normalizeMAC(mac); m != "" {
			out[m] = true
		}
	}
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.GuestNic:
			add(v.Mac)
		case *inventory.PhysNic:
			add(v.Mac)
		case *inventory.LldpNeighbor:
			if strings.EqualFold(v.ChassisIDType, "mac") || looksLikeMAC(v.ChassisID) {
				add(v.ChassisID)
			}
			if strings.EqualFold(v.PortIDType, "mac") || looksLikeMAC(v.PortID) {
				add(v.PortID)
			}
		}
	}
	return out
}

// newRogueFinding builds a SourceRogue finding with a stable, content-derived
// id ("rogue:<check>|<key>"), error severity, never fixable, and the shared
// rogue docs link. nodes is a comma-joined node string (may be empty for a
// signal with no single owning node).
func newRogueFinding(check, detail, nodes string, refs []string, key string) Finding {
	var nodeList []string
	for _, n := range strings.Split(nodes, ",") {
		if n = strings.TrimSpace(n); n != "" {
			nodeList = append(nodeList, n)
		}
	}
	return Finding{
		ID:       "rogue:" + check + "|" + key,
		Source:   SourceRogue,
		Check:    check,
		Severity: SeverityError,
		Detail:   detail,
		DocsLink: rogueDocsLink,
		Nodes:    sortedUnique(nodeList),
		Refs:     sortedUnique(refs),
		Fixable:  false,
	}
}

// macSet returns a set of normalized MACs from ss.
func macSet(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		if m := normalizeMAC(s); m != "" {
			out[m] = true
		}
	}
	return out
}

// setKeys returns m's keys sorted, for deterministic detail/refs rendering.
func setKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// normalizeMAC lower-cases and trims a MAC for case/whitespace-insensitive
// comparison (the same normalization internal/ipam.normMAC applies).
func normalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

// looksLikeMAC reports whether s has the shape of a colon-separated 6-octet
// MAC (aa:bb:cc:dd:ee:ff), used to treat an LLDP chassis/port ID whose type
// field is absent but whose value is clearly a MAC as a known MAC.
func looksLikeMAC(s string) bool {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 6 {
		return false
	}
	for _, p := range parts {
		if len(p) != 2 {
			return false
		}
	}
	return true
}

// firstNonEmpty returns the first non-empty string in vals, or "" if none.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// segmentSuffix renders a " (segment <ref>)" suffix for a non-empty segment
// ref, or "" otherwise, for the unexpected_ra detail string.
func segmentSuffix(seg string) string {
	if seg == "" {
		return ""
	}
	return " (segment " + seg + ")"
}
