package findings

import (
	"sort"
	"strings"
)

// Source names which producer emitted a Finding (docs/features/monitoring.md
// §2/§5: "one findings stream shared with drift, LLDP mismatch, IPAM
// conflicts" plus this task's own health checks).
type Source string

const (
	SourceDrift  Source = "drift"
	SourceLLDP   Source = "lldp"
	SourceIPAM   Source = "ipam"
	SourceHealth Source = "health"
	// SourceProbe (T-806) marks a finding produced by a user-triggered live
	// guest-agent probe (POST /simulate/verify) rather than a continuous
	// background check — additive to the documented drift|lldp|ipam|health
	// enum (docs/api.md's GET /findings finding shape). Currently the sole
	// producer is the persisted sim_divergence check (adapt_probe.go).
	SourceProbe Source = "probe"
	// SourceWireguard (T-1401) marks a finding computed fresh from a live
	// `wg show <if> dump`-equivalent poll of WireGuard's own on-node state
	// (never persisted as truth) — wg_handshake_stale / wg_endpoint_drift.
	SourceWireguard Source = "wireguard"
	// SourceWan (T-1405) marks a finding computed from internal/wan's
	// continuous probe of operator-configured external reference targets —
	// wan_degraded, the "it's the ISP, not the cluster" signal.
	SourceWan Source = "wan"
	// SourceFlow (T-1504) marks a finding computed from internal/flow's
	// service-network attribution (internal/flow.Classifier) — its own
	// top-level source (like SourceProbe), not "health", since it is fed
	// by flow-sample metadata rather than the inventory graph or a polled
	// host/PVE seam. Currently the sole producer is
	// service_traffic_on_wrong_network (health_serviceclass.go).
	SourceFlow Source = "flow"
	// SourceK8s (T-1501) marks a finding produced by internal/k8s's
	// read-only Kubernetes overlay mapping engine — currently the sole
	// producer is k8s_nodeport_exposed_without_fw_rule (adapt_k8s.go).
	SourceK8s Source = "k8s"
	// SourceRogue (T-1605) marks a rogue-service / L2-anomaly detection
	// finding computed fresh from the collectors already gathering L2 data —
	// T-805's ARP/IPv6-neighbor observations, T-1404's IPv6 RA feed, the
	// existing DHCP lease/reservation views, and the inventory graph's own MAC
	// knowledge. Its four checks (rogue_dhcp_server, unexpected_ra,
	// arp_spoof_suspected, unknown_mac_protected_segment) are the stream's
	// most-severe tier (error) and, unlike every other continuously-recomputed
	// producer, hysteresis-exempt: a spoofed/rogue signal is a security event,
	// not a noisy counter to debounce. Detection-only — never a mitigation path
	// (health_rogue.go).
	SourceRogue Source = "rogue"
	// SourceCapacity (T-1606) marks a finding computed by internal/capacity's
	// linear trend over the downsampled capacity_aggregates history —
	// capacity_link_forecast / capacity_ipam_forecast, the "vmbr1 uplink full
	// in ~5 weeks" signal. Its own top-level source (like SourceProbe), not
	// "health", since it is fed by rolled-up long-term aggregates rather than
	// the live inventory graph or a polled host/PVE seam.
	SourceCapacity Source = "capacity"
	// SourceBaseline (T-1601) marks a finding computed from internal/baseline's
	// learned per-guest/per-segment traffic baseline — its own top-level
	// source (like SourceFlow/SourceProbe), since it is fed by a learned
	// statistical summary of flow history rather than the inventory graph or
	// a polled host/PVE seam. Checks are new_port|volume_spike|new_subnet
	// (adapt_baseline.go).
	SourceBaseline Source = "baseline"
	// SourceFederation (T-1407) marks a finding computed from
	// internal/federation's cluster registry crossed with live WireGuard
	// tunnel state — its own top-level source (like SourceFlow/SourceProbe),
	// since it names a federation cluster rather than a node/interface the
	// inventory graph knows about. Currently the sole producer is
	// tunnel_down_peer_unreachable (health_federation_tunnel.go).
	SourceFederation Source = "federation"
	// SourcePeer (T-1906) marks a finding about the peer API's own TLS trust
	// posture — peer_untrusted / peer_unreachable / peer_trust_degraded. Its
	// own top-level source (like SourceFederation), since it describes the
	// cluster's peer transport rather than a node/interface the inventory
	// graph knows about, and because the untrusted case must be legible as a
	// security signal rather than buried in generic "health".
	SourcePeer Source = "peer"
	// SourceStore (T-1905) marks a finding about vnproxd's own app store —
	// currently the sole producer is store_near_capacity
	// (health_storecapacity.go). Its own top-level source (like
	// SourcePeer/SourceFederation), since it describes this daemon's own
	// on-disk footprint rather than a node/interface the inventory graph
	// knows about or the cluster's peer transport.
	SourceStore Source = "store"
	// SourceCert (T-2302) marks a finding about the PVE cluster's own TLS
	// certificates — expiry, SAN coverage, chain to the cluster CA, key
	// strength. Its own top-level source (like SourcePeer, whose transport
	// these certificates authenticate), because a certificate is neither a
	// node/interface the inventory graph knows about nor a live measurement:
	// it is a fact read from pmxcfs, and every check on it is
	// hysteresis-exempt for that reason.
	SourceCert Source = "cert"
	// SourceGitSync (T-2701) marks a finding about the git-backed spec sync:
	// the remote could not be read, the document did not parse, a commit's
	// signature was refused, or intent and reality currently disagree. Its
	// own top-level source (like SourcePeer/SourceStore), because it
	// describes a repository and this daemon's relationship to it rather
	// than a node/interface the inventory graph knows about.
	SourceGitSync Source = "gitsync"
)

// Severity mirrors internal/drift's vocabulary (itself docs/api.md's
// changeset finding vocabulary), so every producer's findings render with
// the same styling in the unified stream.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// severityRank orders Severity for threshold comparisons (notify.go's "fires
// when severity >= threshold") and for picking the "worst" severity when
// more than one finding touches the same map entity (api/topology.go's
// badge painting doesn't need this, but a future summary view might).
var severityRank = map[string]int{
	SeverityInfo:    0,
	SeverityWarning: 1,
	SeverityError:   2,
}

// severityAtLeast reports whether sev meets or exceeds threshold. An
// unrecognized severity string ranks below every known severity (never
// meets a real threshold) rather than panicking or silently matching
// everything.
func severityAtLeast(sev, threshold string) bool {
	return severityRank[sev] >= severityRank[threshold]
}

// Finding is one unified findings-stream entry: the superset of
// docs/api.md's `GET /drift` shape (check/severity/detail/nodes/refs/
// fixable) plus a Source tag and a DocsLink for the "remediation ... docs
// link otherwise" half of docs/features/monitoring.md §5's contract.
//
// Unlike internal/drift.Finding, this type does not carry its own private
// fixOps/fixTitle fields: the fixing-changeset op patch for a fixable
// unified finding is always re-derived on demand by Engine.FixOps
// dispatching back to the owning producer (adapt_drift.go's FixOps strips
// the "drift:" id prefix and calls DriftProvider.FixOps fresh) rather than
// being cached on the adapted copy — the same "always live, never a stale
// cached value" property T-305's own FixOps already established, just
// applied one layer up.
//
// ID is globally stable and unique across every producer: "source:producer-
// id" for drift/lldp (their own producer already computes a stable,
// content-derived key — see adapt_drift.go/adapt_lldp.go) or
// "health:check|refs-or-nodes" for this package's own health checks
// (newHealthFinding, the same scheme internal/drift.newFinding uses).
// Never random or time-based, so re-evaluating unchanged state reproduces
// byte-identical IDs — the property Engine's transition/notification
// tracking (notify.go) and RunLoop's WS-change detection both depend on.
//
//nolint:govet // fieldalignment: wire shape; field order is the documented JSON contract (docs/api.md's GET /findings), not packing — the same precedent internal/api's response DTOs already set.
type Finding struct {
	ID       string   `json:"id"`
	Source   Source   `json:"source"`
	Check    string   `json:"check"`
	Severity string   `json:"severity"`
	Detail   string   `json:"detail"`
	DocsLink string   `json:"docsLink,omitempty"`
	Nodes    []string `json:"nodes"`
	Refs     []string `json:"refs,omitempty"`
	Fixable  bool     `json:"fixable"`

	// AckableAt (T-2604) is the unix instant before which this finding may
	// NOT be acknowledged — 0 (the default, and every finding that predates
	// this field) meaning "acknowledgeable now, as always". It exists for
	// findings whose whole purpose is to force a later review by someone who
	// was not in the room: the break-glass finding cannot be acked for 24
	// hours, so the person who invoked the override cannot silence it on
	// their way out. A producer sets it; AckService.Ack enforces it against
	// the same clock the expiry rule already uses, at write time.
	AckableAt int64 `json:"ackableAt,omitempty"`

	// Remedy is the action vnprox offers to put this finding right
	// (Phase 36), or nil when the finding is detection-only. See the
	// Remediation type for the tier rules — in particular, this field never
	// carries a *network configuration* change: those are Fixable above and
	// flow through internal/change, and nothing here may become a second
	// road to the same destination.
	//
	// Declared by the producer, rendered by every surface. A frontend that
	// decides which button to draw by switching on Check has, by
	// construction, one place per surface for the vocabulary to drift.
	Remedy *Remediation `json:"remedy,omitempty"`

	// Ack is this finding's currently-active acknowledgement (T-2402), or
	// nil. It is never set by a producer: AckService.Decorate attaches it at
	// the API boundary, and an expired acknowledgement leaves it nil. It is
	// deliberately not part of the finding's identity — sortFindings and
	// Engine's transition tracking both ignore it, so acking a finding is not
	// a state change that could fire a notification.
	Ack *Ack `json:"ack,omitempty"`
}

// RemediationKind is which of Phase 36's two producer-declarable tiers a
// remedy belongs to. The third tier — a computed changeset — is NOT
// expressible here on purpose: it is Finding.Fixable, it flows through
// POST /findings/{id}/fix into internal/change, and giving it a second
// representation in this struct would be the first step toward a "Fix"
// button that edits the network without staging anything.
type RemediationKind string

const (
	// RemedyOperational is a host operation that is not network
	// configuration: install a package, start a systemd unit, re-run a
	// poll. There is no PVE config to diff, so there is no changeset to
	// stage — but it is still a mutation, so it carries the same ceremony:
	// explicit confirmation, a capability gate, fixed argv with no
	// operator-supplied strings reaching it, and one audit row per node
	// including refusals and failures. POST /lldp/install (T-605) is the
	// exemplar; docs/security.md records the contract.
	RemedyOperational RemediationKind = "operational"

	// RemedyNavigate carries the operator to the screen where the decision
	// gets made, with context pre-filled. The remedy is a human judgement
	// or configuration vnprox cannot infer, and pretending otherwise would
	// be worse than a link. mgmt_single_path's redundancy wizard is the
	// exemplar.
	RemedyNavigate RemediationKind = "navigate"
)

// The stable Remediation.Action identifiers. Named constants rather than
// literals at each producer, because the frontend registry keys off these
// exact strings and a typo produces a finding whose button silently never
// renders — the worst possible failure for this feature, since the finding
// still looks fine.
const (
	// RemedyActionMgmtRedundancy opens T-703's management-redundancy
	// wizard for Params["node"].
	RemedyActionMgmtRedundancy = "mgmt.redundancy"
	// RemedyActionNavigate sends the operator to Params["to"], an
	// in-app route. For findings whose "what to do" is a screen that
	// already exists.
	RemedyActionNavigate = "navigate"
)

// Remediation is the remedy a producer offers for its finding.
//
// Action is a stable identifier the frontend resolves to a handler
// ("lldp.install", "collector.refresh", "service.start",
// "mgmt.redundancy", "navigate"). It is part of the wire contract in
// docs/api.md and must not be renamed casually — and it, not Check, is what
// a renderer switches on, so that adding a remedy never means editing every
// surface that displays findings.
//
// Params carries whatever the action needs (node, service, source, a
// target route). Deliberately a plain string map rather than a per-action
// struct: this crosses a JSON boundary into a frontend registry that has to
// treat unknown actions as "render nothing" anyway, and a closed union here
// would make every new action a breaking change for older clients rather
// than an ignored one.
//
//nolint:govet // fieldalignment: wire shape; field order is the documented JSON contract (docs/api.md's GET /findings), matching Finding's own precedent above.
type Remediation struct {
	Action string            `json:"action"`
	Kind   RemediationKind   `json:"kind"`
	Label  string            `json:"label"`
	Params map[string]string `json:"params,omitempty"`
}

// sortedUnique returns a sorted copy of ss with duplicates and empty
// strings removed (mirrors internal/drift's helper of the same name).
func sortedUnique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// newHealthFinding builds a SourceHealth Finding with a stable ID derived
// from check plus refs (preferred) or nodes (fallback) — internal/drift's
// newFinding scheme, reused here with a "health:" prefix so a health
// check's key space can never collide with an adapted drift/lldp ID.
func newHealthFinding(check, severity, detail string, nodes, refs []string) Finding {
	nodes = sortedUnique(nodes)
	refs = sortedUnique(refs)
	keyParts := refs
	if len(keyParts) == 0 {
		keyParts = nodes
	}
	return Finding{
		ID:       "health:" + check + "|" + strings.Join(keyParts, ","),
		Source:   SourceHealth,
		Check:    check,
		Severity: severity,
		Detail:   detail,
		Nodes:    nodes,
		Refs:     refs,
	}
}

// sortFindings orders a mixed-source Finding slice deterministically: by ID,
// which already sorts first by source-derived prefix, then by the
// producer's own stable key. Exported for callers (Engine, tests) that
// assemble a slice from more than one producer and need one canonical order.
func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
}
