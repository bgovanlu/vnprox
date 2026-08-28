// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// HoursPerDay is the fixed number of time-of-day byte-volume buckets a
// Profile keeps (0..23, wall-clock UTC hour of day).
const HoursPerDay = 24

const secondsPerHour = 3600

// Learn/Detect knob defaults (T-1601's card).
const (
	// DefaultLearnWindowDays is the default learning window Learn summarizes,
	// capped in practice by whatever flow_samples actually retains per
	// [flows] retention_minutes/max_rows (docs/data-model.md §2).
	DefaultLearnWindowDays = 14
	// DefaultVolumeSpikeMultiple is the default volume_spike threshold: a
	// wall-clock hour's byte volume must be >= this multiple of that
	// hour-of-day's baseline mean+stddev to fire.
	DefaultVolumeSpikeMultiple = 10.0
	// DefaultSubnetPrefixV4/V6 are the prefix lengths a peer IP is aggregated
	// to when forming the observed-subnet set (new_subnet is a per-CIDR, not
	// a per-host, signal — one flow to a genuinely new /24 is noteworthy, a
	// second host inside an already-seen /24 is not).
	DefaultSubnetPrefixV4 = 24
	DefaultSubnetPrefixV6 = 64
	// maxTalkers bounds the top-talkers map a Profile stores so a
	// scanning/noisy Ref can't bloat profile_json without bound.
	maxTalkers = 64
)

// Window is the [Start, End] unix-second learning window a Profile summarizes.
type Window struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// PortKey identifies one observed service port: an IP protocol number plus a
// port number. new_port anomalies are keyed by this pair (tcp/22 and udp/22
// are distinct services).
type PortKey struct {
	Proto int `json:"proto"`
	Port  int `json:"port"`
}

// String renders a PortKey as "proto/port" (e.g. "tcp/22", "17/500" for an
// unnamed protocol) — the human identity used in a new_port anomaly's
// Subject and finding detail.
func (p PortKey) String() string {
	return fmt.Sprintf("%s/%d", flow.ProtoName(p.Proto), p.Port)
}

// HourStat is one time-of-day bucket's byte-volume statistics: the mean and
// population standard deviation of the per-wall-clock-hour byte totals
// observed for that hour-of-day across the learning window, plus the count of
// distinct wall-clock hours that contributed. Both mean and stddev are over
// the SAME per-hour granularity Detect compares against, so a spike test
// ("this one hour vs a typical hour") is scale-consistent.
type HourStat struct {
	Mean   float64 `json:"mean"`
	Stddev float64 `json:"stddev"`
	Count  int     `json:"count"`
}

// Profile is a learned traffic baseline for one inventory Ref: the statistical
// summary Learn computes over a window. Talkers maps a peer identity (the
// peer's inventory Ref when known, else its IP) to total observed bytes;
// Ports is the sorted set of observed service ports; Subnets is the sorted set
// of observed peer CIDRs; Hours is the per-hour-of-day byte-volume histogram.
type Profile struct {
	Ref     string                `json:"ref"`
	Talkers map[string]int64      `json:"talkers"`
	Ports   []PortKey             `json:"ports"`
	Subnets []string              `json:"subnets"`
	Window  Window                `json:"window"`
	Hours   [HoursPerDay]HourStat `json:"hours"`
}

// Empty reports whether this Profile learned nothing for its Ref (no matching
// flows in the window). Detect treats an empty Profile as "no baseline yet"
// and raises nothing; the learn job skips persisting an empty Profile — the
// two together make cold-start silent (T-1601 AC5).
func (p Profile) Empty() bool {
	return len(p.Talkers) == 0
}

// Marshal serializes a Profile to the JSON stored in baseline_profiles.
// profile_json.
func Marshal(p Profile) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("baseline: marshaling profile for %s: %w", p.Ref, err)
	}
	return string(b), nil
}

// Unmarshal is Marshal's inverse.
func Unmarshal(s string) (Profile, error) {
	var p Profile
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Profile{}, fmt.Errorf("baseline: unmarshaling profile: %w", err)
	}
	return p, nil
}

// refMatch is one flow record's contribution to a ref's baseline: the peer's
// identity/IP and the flow's service port (if any).
type refMatch struct {
	peerID  string  // peer inventory Ref if known, else peer IP
	peerIP  string  // raw peer IP (for subnet aggregation)
	port    PortKey // valid only when hasPort
	hasPort bool
}

// recordForRef reports whether rec involves ref and, if so, extracts the peer
// endpoint and the flow's service port. The "service port" is the record's
// DESTINATION port — the side being connected to (a listening service),
// whichever direction the flow runs — and ephemeral source ports are
// deliberately excluded: a client's random high source port carries no signal
// and would otherwise make every new connection look like a "new port".
func recordForRef(rec flow.Record, ref string) (refMatch, bool) {
	if ref == "" {
		return refMatch{}, false
	}
	var m refMatch
	switch {
	case rec.SrcRef == ref:
		m.peerIP = rec.DstIP
		m.peerID = rec.DstRef
	case rec.DstRef == ref:
		m.peerIP = rec.SrcIP
		m.peerID = rec.SrcRef
	default:
		return refMatch{}, false
	}
	if m.peerID == "" {
		m.peerID = m.peerIP
	}
	if rec.DstPort > 0 {
		m.port = PortKey{Proto: rec.Proto, Port: rec.DstPort}
		m.hasPort = true
	}
	return m, true
}

// PeerSubnet aggregates a peer IP to its containing /v4bits (IPv4) or /v6bits
// (IPv6) network, rendered as a CIDR string — the exported form of the same
// aggregation Learn/Detect use to key the observed-subnet set. T-1602's
// microsegmentation planner reuses this verbatim so its per-subnet rule
// grouping and its anomaly-exclusion subnet matching share ONE aggregation
// with baseline detection (never a re-derived, drift-prone second copy). ok is
// false for an unparseable address.
func PeerSubnet(ipStr string, v4bits, v6bits int) (string, bool) {
	return peerSubnet(ipStr, v4bits, v6bits)
}

// HourSubject renders the wall-clock hour bucket containing unixSeconds as the
// same stable subject string a volume_spike Anomaly carries (e.g.
// "2024-01-15T14:00Z"). Exported so T-1602's planner can decide whether a given
// flow falls inside a volume_spike anomaly's flagged hour using baseline's own
// bucket semantics rather than re-deriving them.
func HourSubject(unixSeconds int64) string {
	return hourSubject(unixSeconds / secondsPerHour)
}

// peerSubnet aggregates a peer IP to its /v4bits (IPv4) or /v6bits (IPv6)
// containing network, rendered as a CIDR string (e.g. "10.0.0.0/24"). ok is
// false for an unparseable address.
func peerSubnet(ipStr string, v4bits, v6bits int) (string, bool) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", false
	}
	if v4 := ip.To4(); v4 != nil {
		mask := net.CIDRMask(v4bits, 32)
		return (&net.IPNet{IP: v4.Mask(mask), Mask: mask}).String(), true
	}
	mask := net.CIDRMask(v6bits, 128)
	return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String(), true
}

// Learn computes a Profile for ref over window from records. Records outside
// [window.Start, window.End] or not involving ref are ignored. The result is
// deterministic: identical inputs yield a byte-identical serialized Profile
// (map keys sort on JSON encode; Ports/Subnets are explicitly sorted).
func Learn(records []flow.Record, ref string, window Window) Profile {
	prof := Profile{Ref: ref, Window: window, Talkers: map[string]int64{}}
	if ref == "" {
		return prof
	}

	portSet := map[PortKey]bool{}
	subnetSet := map[string]bool{}
	bytesByHour := map[int64]int64{}

	for _, rec := range records {
		if rec.At < window.Start || rec.At > window.End {
			continue
		}
		m, ok := recordForRef(rec, ref)
		if !ok {
			continue
		}
		prof.Talkers[m.peerID] += rec.Bytes
		if m.hasPort {
			portSet[m.port] = true
		}
		if sn, ok := peerSubnet(m.peerIP, DefaultSubnetPrefixV4, DefaultSubnetPrefixV6); ok {
			subnetSet[sn] = true
		}
		bytesByHour[rec.At/secondsPerHour] += rec.Bytes
	}

	valuesByHOD := make([][]int64, HoursPerDay)
	for absHour, b := range bytesByHour {
		hod := hourOfDay(absHour)
		valuesByHOD[hod] = append(valuesByHOD[hod], b)
	}
	for hod := 0; hod < HoursPerDay; hod++ {
		prof.Hours[hod] = statOf(valuesByHOD[hod])
	}

	prof.Ports = sortedPorts(portSet)
	prof.Subnets = sortedStrings(subnetSet)
	prof.Talkers = topTalkers(prof.Talkers, maxTalkers)
	return prof
}

// hourOfDay maps an absolute wall-clock hour index (unix seconds / 3600) to
// its UTC hour-of-day bucket 0..23.
func hourOfDay(absHour int64) int {
	return int(((absHour % HoursPerDay) + HoursPerDay) % HoursPerDay)
}

// statOf computes the mean and population standard deviation of vals (empty
// vals yields the zero HourStat, i.e. "no baseline for this hour-of-day").
func statOf(vals []int64) HourStat {
	if len(vals) == 0 {
		return HourStat{}
	}
	var sum float64
	for _, v := range vals {
		sum += float64(v)
	}
	mean := sum / float64(len(vals))
	var sq float64
	for _, v := range vals {
		d := float64(v) - mean
		sq += d * d
	}
	return HourStat{Mean: mean, Stddev: math.Sqrt(sq / float64(len(vals))), Count: len(vals)}
}

func sortedPorts(set map[PortKey]bool) []PortKey {
	out := make([]PortKey, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		return out[i].Port < out[j].Port
	})
	return out
}

func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// topTalkers returns the n highest-byte talkers from all, ties broken by peer
// identity for determinism. Keeps the map shape (identity -> bytes).
func topTalkers(all map[string]int64, n int) map[string]int64 {
	if len(all) <= n {
		return all
	}
	type kv struct {
		id    string
		bytes int64
	}
	pairs := make([]kv, 0, len(all))
	for id, b := range all {
		pairs = append(pairs, kv{id: id, bytes: b})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].bytes != pairs[j].bytes {
			return pairs[i].bytes > pairs[j].bytes
		}
		return pairs[i].id < pairs[j].id
	})
	out := make(map[string]int64, n)
	for _, p := range pairs[:n] {
		out[p.id] = p.bytes
	}
	return out
}
