package microseg

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Planner knob defaults.
const (
	// DefaultCoverageThreshold is the fraction of observed-good BYTES the
	// covering set must reach before the long tail is left uncovered (default
	// 99.5%, this card's stated default). Never rounded to 1.0: the Proposal
	// always reports the real coverage and the uncovered-flow count.
	DefaultCoverageThreshold = 0.995
	// DefaultSubnetPrefixV4/V6 mirror internal/baseline's own peer-subnet
	// aggregation (a rule per /24, per /64), so the planner's per-subnet rule
	// grouping and baseline anomaly detection agree on what "same subnet"
	// means.
	DefaultSubnetPrefixV4 = baseline.DefaultSubnetPrefixV4
	DefaultSubnetPrefixV6 = baseline.DefaultSubnetPrefixV6
	// DefaultDenyAction is the action of the trailing match-all rule each
	// governed direction gets, which turns the ordered ACCEPT allow-list into a
	// default-deny policy expressible entirely as fw.rule.create ops (no
	// separate default-policy op — see Stage). DROP (silent) over REJECT is the
	// microsegmentation convention.
	DefaultDenyAction = "DROP"
)

// Subject identifies what a proposal governs. GuestRef is the guest whose
// observed flows drive synthesis — matched against flow SrcRef/DstRef, so it is
// the guest's own inventory Ref (Kind KindGuest). RulesetRef is the firewall
// ruleset Stage emits ops against (Kind KindFwRuleset): the caller resolves it
// from live inventory because microseg cannot derive the "guest/<kind>/<vmid>"
// ruleset id from a bare guest ref (which carries no qemu/lxc kind).
type Subject struct {
	GuestRef   inventory.Ref
	RulesetRef inventory.Ref
}

// Config tunes Propose. A zero value is valid — withDefaults fills every
// non-positive/empty field from the package defaults.
type Config struct {
	DenyAction        string
	CoverageThreshold float64
	SubnetPrefixV4    int
	SubnetPrefixV6    int
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		CoverageThreshold: DefaultCoverageThreshold,
		SubnetPrefixV4:    DefaultSubnetPrefixV4,
		SubnetPrefixV6:    DefaultSubnetPrefixV6,
		DenyAction:        DefaultDenyAction,
	}
}

func (c Config) withDefaults() Config {
	if c.CoverageThreshold <= 0 || c.CoverageThreshold > 1 {
		c.CoverageThreshold = DefaultCoverageThreshold
	}
	if c.SubnetPrefixV4 <= 0 {
		c.SubnetPrefixV4 = DefaultSubnetPrefixV4
	}
	if c.SubnetPrefixV6 <= 0 {
		c.SubnetPrefixV6 = DefaultSubnetPrefixV6
	}
	if c.DenyAction == "" {
		c.DenyAction = DefaultDenyAction
	}
	return c
}

func (c Config) detectConfig() baseline.DetectConfig {
	return baseline.DetectConfig{
		SubnetPrefixV4: c.SubnetPrefixV4,
		SubnetPrefixV6: c.SubnetPrefixV6,
	}
}

// Existing carries what the planner already knows PVE effectively enforces, so
// it does not propose a rule the guest already has. View is the guest's current
// resolved firewall view (optional); when set, a candidate group whose
// representative flow the existing view ALREADY accepts is suppressed (its
// bytes still count as covered — the traffic is permitted, just not by a new
// rule). Analytics is T-1006's firewall-log analytics, carried for the review
// surface (T-1603) as advisory context; suppression itself is decided by the
// resolved view through the shared evaluator, not by log hit-counts, which
// cannot by themselves prove which (proto,port,subnet) a rule covers.
type Existing struct {
	View      *fw.ResolvedView
	Analytics *fwlog.Analytics
}

// Proposal is Propose's result. Rules is the ordered minimal policy (the ACCEPT
// allow-list followed by one trailing match-all deny per governed direction).
// CoveragePct/UncoveredFlowCount are the honesty fields — the exact fraction of
// observed-good bytes the rules cover and how many observed-good flows fall in
// the deliberately-uncovered tail. The remaining counters make the synthesis
// auditable end to end.
type Proposal struct {
	Subject               Subject
	Directions            []string
	Rules                 []inventory.FwRule
	CoveragePct           float64
	ObservedGoodBytes     int64
	CoveredBytes          int64
	ObservedGoodFlowCount int
	UncoveredFlowCount    int
	ExcludedAnomalyFlows  int
	AlreadyCoveredGroups  int
}

// flowMeta is one corpus record projected onto the target: its direction
// relative to the target, the peer's address, the flow's service tuple, and the
// observation time/bytes the report and volume-spike exclusion need (the raw
// flow.Record itself is not retained — projectFlow extracts everything downstream
// uses).
type flowMeta struct {
	direction string // "in" (target is dst) | "out" (target is src)
	peerIP    string
	subnet    string
	at        int64
	bytes     int64
	proto     int
	port      int
	hasPort   bool
	hasSubnet bool
}

// groupKey is the identity a minimal-covering-set rule collapses flows to.
type groupKey struct {
	direction string
	subnet    string
	proto     int
	port      int
}

type group struct {
	key   groupKey
	rep   flowMeta // a representative flow, for existing-policy suppression checks
	bytes int64
	flows int
}

// Propose computes the minimal covering-set policy for subj over corpus. profile
// is T-1601's learned baseline for the same guest (used ONLY to identify — and
// exclude — anomalous flows, so the policy never legitimizes a flagged flow);
// pass an empty Profile to skip anomaly exclusion (then every corpus flow
// involving the target counts as observed-good). existing suppresses rules PVE
// already effectively has. The result is deterministic for identical inputs.
func Propose(subj Subject, corpus []flow.Record, profile baseline.Profile, existing Existing, cfg Config) Proposal {
	cfg = cfg.withDefaults()
	target := subj.GuestRef.String()

	excl := newExcluder(profile, corpus, cfg)

	groups := map[groupKey]*group{}
	var observedGoodBytes int64
	observedGoodFlows := 0
	excluded := 0
	for _, rec := range corpus {
		m, ok := projectFlow(rec, target, cfg)
		if !ok {
			continue
		}
		if excl.flags(m) {
			excluded++
			continue
		}
		if !m.hasPort || !m.hasSubnet {
			// A flow with no identifiable service port or unparseable peer
			// address cannot be collapsed into a precise (proto,port,subnet)
			// rule. Counting it observed-good-but-uncovered keeps coverage
			// honest rather than fabricating an any-port/any-peer allow.
			observedGoodFlows++
			observedGoodBytes += rec.Bytes
			continue
		}
		observedGoodFlows++
		observedGoodBytes += rec.Bytes
		key := groupKey{direction: m.direction, subnet: m.subnet, proto: m.proto, port: m.port}
		g := groups[key]
		if g == nil {
			g = &group{key: key, rep: m}
			groups[key] = g
		}
		g.bytes += rec.Bytes
		g.flows++
	}

	ordered := sortedGroups(groups)
	included, coveredBytes := selectCovering(ordered, observedGoodBytes, cfg.CoverageThreshold)

	prop := Proposal{
		Subject:               subj,
		ObservedGoodBytes:     observedGoodBytes,
		CoveredBytes:          coveredBytes,
		ObservedGoodFlowCount: observedGoodFlows,
		ExcludedAnomalyFlows:  excluded,
	}
	// Uncovered = every observed-good flow not represented by an included,
	// non-suppressed group (the deliberately-dropped tail plus any
	// portless/unparseable flows counted above).
	includedSet := map[groupKey]bool{}
	for _, g := range included {
		includedSet[g.key] = true
	}
	coveredFlowCount := 0
	for _, g := range included {
		coveredFlowCount += g.flows
	}
	prop.UncoveredFlowCount = observedGoodFlows - coveredFlowCount

	if observedGoodBytes > 0 {
		prop.CoveragePct = float64(coveredBytes) / float64(observedGoodBytes) * 100
	}

	prop.Rules, prop.Directions, prop.AlreadyCoveredGroups = buildRules(included, existing, cfg)
	return prop
}

// projectFlow projects rec onto target: whether it involves target, in which
// direction, and its peer/service tuple. A self-flow (target both src and dst)
// is skipped — a guest talking to itself is not a segmentation-relevant edge.
func projectFlow(rec flow.Record, target string, cfg Config) (flowMeta, bool) {
	if target == "" {
		return flowMeta{}, false
	}
	var m flowMeta
	m.at = rec.At
	m.bytes = rec.Bytes
	switch {
	case rec.SrcRef == target && rec.DstRef == target:
		return flowMeta{}, false
	case rec.SrcRef == target:
		m.direction = "out"
		m.peerIP = rec.DstIP
	case rec.DstRef == target:
		m.direction = "in"
		m.peerIP = rec.SrcIP
	default:
		return flowMeta{}, false
	}
	m.proto = rec.Proto
	if rec.DstPort > 0 {
		m.port = rec.DstPort
		m.hasPort = true
	}
	if sn, ok := baseline.PeerSubnet(m.peerIP, cfg.SubnetPrefixV4, cfg.SubnetPrefixV6); ok {
		m.subnet = sn
		m.hasSubnet = true
	}
	return m, true
}

// sortedGroups returns groups in a deterministic order: by descending bytes
// (the covering set greedily takes the heaviest first), ties broken by the
// group key so identical inputs always yield identical proposals.
func sortedGroups(groups map[groupKey]*group) []*group {
	out := make([]*group, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].bytes != out[j].bytes {
			return out[i].bytes > out[j].bytes
		}
		return keyLess(out[i].key, out[j].key)
	})
	return out
}

// selectCovering greedily includes heaviest groups until cumulative bytes reach
// threshold*totalBytes, returning the included groups and their byte total. The
// tail past the threshold is deliberately excluded (reported, never rounded
// up). A group is always taken whole — a rule either covers its subnet/port or
// it does not.
func selectCovering(ordered []*group, totalBytes int64, threshold float64) ([]*group, int64) {
	if totalBytes <= 0 {
		return nil, 0
	}
	target := float64(totalBytes) * threshold
	var covered int64
	var included []*group
	for _, g := range ordered {
		if float64(covered) >= target {
			break
		}
		included = append(included, g)
		covered += g.bytes
	}
	return included, covered
}

// buildRules turns the included groups into the ordered rule list: the ACCEPT
// allow-list (deterministically ordered) followed by one trailing match-all
// deny per governed direction, so the whole default-deny policy is expressible
// as fw.rule.create ops alone. A group the existing policy already accepts is
// suppressed (no duplicate ACCEPT) but its direction still gets governed.
func buildRules(included []*group, existing Existing, cfg Config) (rules []inventory.FwRule, directions []string, suppressed int) {
	// Deterministic ACCEPT ordering by group key.
	sort.Slice(included, func(i, j int) bool { return keyLess(included[i].key, included[j].key) })

	governed := map[string]bool{}
	pos := 0
	for _, g := range included {
		governed[g.key.direction] = true
		if alreadyAccepted(existing.View, g, cfg) {
			suppressed++
			continue
		}
		rules = append(rules, acceptRule(g.key, pos))
		pos++
	}

	// Trailing match-all deny, one per governed direction, after every ACCEPT.
	for _, dir := range []string{"in", "out"} {
		if !governed[dir] {
			continue
		}
		directions = append(directions, dir)
		rules = append(rules, inventory.FwRule{
			Direction: dir,
			Action:    cfg.DenyAction,
			Comment:   "vnprox microseg: default-deny (everything else was noise)",
			Pos:       pos,
			Enabled:   true,
		})
		pos++
	}
	return rules, directions, suppressed
}

// acceptRule builds the ACCEPT rule for one covered group. An "in" rule
// constrains the peer as Source (traffic arriving at the guest); an "out" rule
// constrains the peer as Dest (traffic the guest initiates). Dport carries the
// service port both ways (the listening side).
func acceptRule(k groupKey, pos int) inventory.FwRule {
	r := inventory.FwRule{
		Direction: k.direction,
		Action:    "ACCEPT",
		Proto:     flow.ProtoName(k.proto),
		Dport:     strconv.Itoa(k.port),
		Comment:   fmt.Sprintf("vnprox microseg: observed %s to %s", flow.ProtoName(k.proto), k.subnet),
		Pos:       pos,
		Enabled:   true,
	}
	if k.direction == "in" {
		r.Source = k.subnet
	} else {
		r.Dest = k.subnet
	}
	return r
}

func keyLess(a, b groupKey) bool {
	if a.direction != b.direction {
		return a.direction < b.direction
	}
	if a.proto != b.proto {
		return a.proto < b.proto
	}
	if a.port != b.port {
		return a.port < b.port
	}
	return a.subnet < b.subnet
}
