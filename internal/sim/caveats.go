package sim

import "fmt"

// Caveat codes. These are the machine-stable keys T-504's UI keys off to
// style/group the honesty disclosures. Each corresponds to a row in the
// feature→evaluated|caveated inventory in planning/reports/T-503.md (AC3).
const (
	// CodeSimulated is the standing "these are simulated, not live packets"
	// label docs/features/firewall.md §5/§6 mandates on every result.
	CodeSimulated = "simulated"
	// CodeConntrack notes that established-flow / conntrack behavior
	// (stateful accept of return traffic, related connections) is not
	// modeled — the engine evaluates the configured rule set statically.
	CodeConntrack = "conntrack"
	// CodeGuestInternalFirewall notes that a firewall running *inside* a
	// guest OS is invisible to the engine (it only sees pve-firewall).
	CodeGuestInternalFirewall = "guest-internal-firewall"
	// CodeSNATAsymmetry notes that a SNAT'd egress path's return traffic is
	// conntrack-dependent, so a forward "allow" does not by itself prove the
	// reply is deliverable.
	CodeSNATAsymmetry = "snat-asymmetry"
	// CodeGuestAgentIP notes a resolved IP came from a low-confidence
	// (guest-agent, runtime) source rather than configuration.
	CodeGuestAgentIP = "guest-agent-ip"
	// CodeGuestIPUnknown notes a firewall decision could not be completed
	// because a guest endpoint's IP is not known to the engine and a rule
	// along the path restricts by address. Blocker (forces Indeterminate).
	CodeGuestIPUnknown = "guest-ip-unknown"
	// CodeFwClusterHostGuest flags that a firewall verdict inherited
	// internal/fw.Resolve's documented simplification: cluster [RULES] are
	// applied directly to a guest's resolved view, whereas real pve-firewall
	// only applies them to the node host chain (reaching a guest chain only
	// via a security group). Warning — needs hardware validation.
	CodeFwClusterHostGuest = "fw-cluster-host-guest-simplification"
	// CodeLLDPTrunkMismatch notes the configured bridge VID set permits a
	// VLAN that the switch's LLDP advertisement does not list as trunked —
	// an advisory cross-check, not a verdict authority.
	CodeLLDPTrunkMismatch = "lldp-trunk-cross-check"
	// CodeNotEvaluated is the AC5 marker: a feature/entity kind the engine
	// does not evaluate was on the path. Blocker (forces Indeterminate).
	CodeNotEvaluated = "not-evaluated"
	// CodeNodeFirewall notes a node-scope (host-chain) firewall ruleset
	// exists and is enabled; guest-to-guest bridged/forwarded traffic does
	// not traverse the host INPUT/OUTPUT chains, so those rules are not
	// evaluated for this path.
	CodeNodeFirewall = "node-firewall-not-on-path"
	// CodeOVS notes an Open vSwitch bridge/bond is on the path; the engine's
	// L2 VLAN reasoning is validated against Linux-bridge semantics only.
	CodeOVS = "ovs-l2"
)

// Feature names for the not-evaluated / honesty inventory. Kept as
// constants so the report's grep-able inventory and the code cannot drift.
const (
	FeatureGuestIP           = "guest IP resolution (IPAM / guest-agent) not carried in inventory"
	FeatureConntrack         = "connection tracking / stateful return traffic"
	FeatureGuestInternalFW   = "guest-internal (in-OS) firewalls"
	FeatureNodeHostChain     = "node-scope (host INPUT/OUTPUT chain) firewall rules"
	FeatureQinQInner         = "QinQ inner-tag (customer VLAN) L2 isolation"
	FeatureOVSVlan           = "Open vSwitch VLAN/trunk semantics"
	FeatureUnknownEntityKind = "path traverses an entity kind the simulator does not model"
	FeatureExternalRouting   = "upstream/physical-network routing beyond the PVE node's own gateway"
)

func infoCaveat(code, msg string) Caveat {
	return Caveat{Code: code, Severity: CaveatInfo, Message: msg}
}

func warnCaveat(code, msg string) Caveat {
	return Caveat{Code: code, Severity: CaveatWarning, Message: msg}
}

func blockerCaveat(code, msg string) Caveat {
	return Caveat{Code: code, Severity: CaveatBlocker, Message: msg}
}

// notEvaluated builds the AC5 blocker caveat naming an un-evaluated feature.
func notEvaluated(feature, detail string) Caveat {
	msg := fmt.Sprintf("not evaluated: %s", feature)
	if detail != "" {
		msg += " — " + detail
	}
	return Caveat{Code: CodeNotEvaluated, Severity: CaveatBlocker, Message: msg, Feature: feature}
}

// standingCaveats are the always-present honesty notes docs/features/
// firewall.md §5/§6 require on every result: the "simulated" label plus the
// two nuance classes the engine structurally cannot see (conntrack and
// guest-internal firewalls). They are appended last so a fully-confident
// allow still discloses them.
func standingCaveats() []Caveat {
	return []Caveat{
		infoCaveat(CodeSimulated, "Simulated over configured state, not a live packet probe. Runtime conditions (links down, ARP, MTU blackholes) are not observed."),
		infoCaveat(CodeConntrack, "Connection tracking is not modeled: established/related return traffic and stateful accepts are not evaluated."),
		infoCaveat(CodeGuestInternalFirewall, "A firewall running inside the guest OS is invisible to this simulator; only pve-firewall is evaluated."),
	}
}
