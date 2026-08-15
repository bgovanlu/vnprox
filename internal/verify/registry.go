package verify

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// CheckFunc is one check's body.
type CheckFunc func(ctx context.Context, d Deps) Outcome

// Check is a registry entry: a claim in docs/status-matrix.md §2, the
// hardware it takes to test that claim, and the code that tests it.
//
//nolint:govet // fieldalignment: read as a table, ordered like one.
type Check struct {
	// ID is stable and appears in the report, so it is a contract once
	// shipped — T-2503's telemetry reduction keys on it.
	ID string
	// MatrixRow is the docs/status-matrix.md §2 row this check backs. AC1: a
	// row number that does not exist fails the build, and a row claiming `V`
	// that no check names also fails the build.
	MatrixRow int
	// Area must equal that row's feature-area title. Carrying it here rather
	// than only the number means renaming a row without revisiting its checks
	// is a build failure rather than a silent mis-attribution.
	Area string
	// Suite decides when this check runs.
	Suite Suite
	// MinNodes is how many *online* cluster nodes the check needs. Below it,
	// the check skips naming the count it saw.
	MinNodes int
	// Precondition states, in one sentence, the hardware this check needs in
	// order to mean anything (AC7). It is printed next to every skip, so an
	// operator who wants to help knows what to go and get.
	Precondition string
	// Run is the check.
	Run CheckFunc
}

// Checks is the whole registry, in report order: read-only cluster reads
// first, then multi-node behaviour, then the destructive suite.
//
// Every entry here is a row in docs/status-matrix.md §2 that this project
// makes a claim about and, until now, could not demonstrate. The twelve rows
// marked `B` (blocked — needs hardware we do not have) each have at least one
// check; so does every row already marked `V`, so that a validated row cannot
// quietly lose the thing that validated it (AC1's second direction).
func Checks() []Check {
	return []Check{
		// --- Row 3: change engine ------------------------------------------
		{
			ID:           "change.applied_changeset_committed",
			MatrixRow:    3,
			Area:         "Change engine (stage→validate→diff→apply→confirm)",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real PVE node on which at least one changeset has been applied through vnprox (stage → validate → apply → confirm)",
			Run:          checkChangeCommitted,
		},
		{
			ID:           "change.multinode_apply_rollback",
			MatrixRow:    3,
			Area:         "Change engine (stage→validate→diff→apply→confirm)",
			Suite:        SuiteDestructive,
			MinNodes:     2,
			Precondition: "two or more online PVE nodes and a non-management interface on each that may be reprogrammed with its own current value",
			Run:          checkChangeMultinodeApplyRollback,
		},

		// --- Row 4: commit-confirm ----------------------------------------
		{
			ID:           "commitconfirm.window_within_ticket_lifetime",
			MatrixRow:    4,
			Area:         "Commit-confirm + unattended rollback",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real PVE node running vnproxd, whose configured commit-confirm window must fit inside a real PVE ticket's 2h lifetime",
			Run:          checkCommitConfirmWindow,
		},
		{
			ID:           "commitconfirm.unattended_rollback_fires",
			MatrixRow:    4,
			Area:         "Commit-confirm + unattended rollback",
			Suite:        SuiteDestructive,
			MinNodes:     1,
			Precondition: "a real PVE node and permission to apply a changeset and deliberately never confirm it, letting the window expire",
			Run:          checkCommitConfirmUnattendedRollback,
		},

		// --- Row 6: bridges, bonds, VLANs ---------------------------------
		{
			ID:           "iface.lacp_partner_observed",
			MatrixRow:    6,
			Area:         "Bridges, bonds, VLANs, interfaces",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real 802.3ad bond on a real node, cabled to a switch with LACP configured on the matching port-channel",
			Run:          checkLACPPartner,
		},

		// --- Row 14: external subnets -------------------------------------
		{
			ID:           "ipam.external_subnet_provenance",
			MatrixRow:    14,
			Area:         "External subnets + NetBox/phpIPAM sync",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install with at least one registered external subnet; a NetBox/phpIPAM instance to sync from exercises the external provenance path",
			Run:          checkExternalSubnetProvenance,
		},

		// --- Row 21: drift -------------------------------------------------
		{
			ID:           "drift.config_vs_live",
			MatrixRow:    21,
			Area:         "Drift detection (config-vs-live, node-vs-node)",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real PVE node whose /etc/network/interfaces and running kernel state the daemon can both read",
			Run:          checkDriftConfigVsLive,
		},
		{
			ID:           "drift.node_vs_node",
			MatrixRow:    21,
			Area:         "Drift detection (config-vs-live, node-vs-node)",
			Suite:        SuiteMultinode,
			MinNodes:     2,
			Precondition: "two or more online PVE nodes; a deliberate same-named-bridge divergence between them exercises the cross-node comparison",
			Run:          checkDriftNodeVsNode,
		},

		// --- Row 24: flows -------------------------------------------------
		{
			ID:           "flows.records_ingested",
			MatrixRow:    24,
			Area:         "Flows (sFlow/NetFlow/IPFIX) + explorer",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real sFlow/NetFlow/IPFIX exporter (a switch or the node itself) pointed at this node's configured flow listener port",
			Run:          checkFlowsIngested,
		},

		// --- Row 31: packet capture ---------------------------------------
		{
			ID:           "capture.af_packet_backend",
			MatrixRow:    31,
			Area:         "Packet capture + BPF builder",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real capture run on a real interface with live traffic, by a vnproxd holding CAP_NET_RAW",
			Run:          checkCaptureAFPacket,
		},

		// --- Row 32: LLDP --------------------------------------------------
		{
			ID:           "lldp.neighbors_match_pve_interfaces",
			MatrixRow:    32,
			Area:         "LLDP discovery + ports view",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real node running lldpd, cabled to a switch that advertises LLDP on the connected ports",
			Run:          checkLLDPNeighbors,
		},

		// --- Rows 41/42: federation ---------------------------------------
		{
			ID:           "federation.credential_never_echoed",
			MatrixRow:    41,
			Area:         "Federation (multi-cluster)",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install with at least one attached remote cluster, whose credential was sealed on attach",
			Run:          checkFederationCredentialNeverEchoed,
		},
		{
			ID:           "federation.remote_cluster_round_trip",
			MatrixRow:    41,
			Area:         "Federation (multi-cluster)",
			Suite:        SuiteMultinode,
			MinNodes:     1,
			Precondition: "two real PVE clusters, the second attached to this one through the federation surface",
			Run:          checkFederationRoundTrip,
		},
		{
			ID:           "federation.ipam_conflicts",
			MatrixRow:    42,
			Area:         "Cross-cluster IPAM conflicts",
			Suite:        SuiteMultinode,
			MinNodes:     1,
			Precondition: "two real PVE clusters with SDN subnets configured in both; an overlapping CIDR across them exercises the conflict path",
			Run:          checkFederationIPAMConflicts,
		},

		// --- Row 43: WireGuard ---------------------------------------------
		{
			ID:           "wireguard.private_key_never_returned",
			MatrixRow:    43,
			Area:         "WireGuard cluster interconnect",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install with at least one WireGuard tunnel whose private key was generated on the owning node",
			Run:          checkWireGuardNoPrivateKey,
		},
		{
			ID:           "wireguard.tunnel_handshake",
			MatrixRow:    43,
			Area:         "WireGuard cluster interconnect",
			Suite:        SuiteMultinode,
			MinNodes:     1,
			Precondition: "a real WireGuard tunnel between two real clusters, with a routable path between their endpoints",
			Run:          checkWireGuardHandshake,
		},

		// --- Row 44: switch push -------------------------------------------
		{
			ID:           "switch.real_device_reachable",
			MatrixRow:    44,
			Area:         "Switch config push (opt-in, 2-key)",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real managed switch registered in [switches] with the daemon-level enable flag set — the driver has otherwise only ever run against internal/switchmock",
			Run:          checkSwitchReachable,
		},

		// --- Row 48: SR-IOV -------------------------------------------------
		{
			ID:           "sriov.vf_capable_nic_present",
			MatrixRow:    48,
			Area:         "SR-IOV VF lifecycle",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real SR-IOV-capable NIC with IOMMU/VT-d enabled in firmware and the kernel booted with intel_iommu=on (or the AMD equivalent)",
			Run:          checkSRIOVCapableNIC,
		},
		{
			ID:           "sriov.vf_lifecycle",
			MatrixRow:    48,
			Area:         "SR-IOV VF lifecycle",
			Suite:        SuiteDestructive,
			MinNodes:     1,
			Precondition: "a real SR-IOV NIC with spare VFs that may be provisioned and released, carrying no production guest traffic",
			Run:          checkSRIOVVFLifecycle,
		},

		// --- Row 56: HA -----------------------------------------------------
		{
			ID:           "ha.lease_and_replication",
			MatrixRow:    56,
			Area:         "HA active/standby",
			Suite:        SuiteMultinode,
			MinNodes:     2,
			Precondition: "two or more real nodes running vnproxd with HA configured, so one holds the lease and the other replicates",
			Run:          checkHALeaseAndReplication,
		},
		{
			ID:           "ha.failover",
			MatrixRow:    56,
			Area:         "HA active/standby",
			Suite:        SuiteDestructive,
			MinNodes:     2,
			Precondition: "two or more real nodes running vnproxd with HA configured, and permission to stop the active daemon and watch the standby promote",
			Run:          checkHAFailover,
		},

		// --- Row 61: backup --------------------------------------------------
		{
			ID:           "backup.archive_round_trip",
			MatrixRow:    61,
			Area:         "Backup / restore of vnprox state",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install with a populated store on disk and vnproxctl on PATH",
			Run:          checkBackupRoundTrip,
		},

		// --- Row 62: support bundle ------------------------------------------
		{
			ID:           "supportbundle.contains_no_secret",
			MatrixRow:    62,
			Area:         "Support bundle export",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install holding a real session key and a real PVE token, so the redaction is tested against actual credentials rather than fixtures",
			Run:          checkSupportBundleNoSecret,
		},

		// --- Row 65: peer CA pinning ------------------------------------------
		{
			ID:           "peer.ca_pins_real_chain",
			MatrixRow:    65,
			Area:         "Peer-API CA pinning + verify-names",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real pmxcfs mount carrying this cluster's own /etc/pve/pve-root-ca.pem and the node's PVE-issued leaf certificate",
			Run:          checkPeerCAPinsRealChain,
		},

		// --- Row 66: certificate management ----------------------------------
		{
			ID:           "certs.inventory_matches_pmxcfs",
			MatrixRow:    66,
			Area:         "Certificate management (new)",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real pmxcfs mount with each node's own pve-ssl.pem present under /etc/pve/nodes",
			Run:          checkCertsInventory,
		},

		// --- Row 73: Mobile PWA + push ----------------------------------------
		{
			ID:           "pwa.servable",
			MatrixRow:    73,
			Area:         "Mobile PWA + push",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install reachable over HTTPS; the on-device half (install on real iOS/Android, one push delivered through FCM/APNs/autopush) stays a human item — needs-hardware-validation.md §T-2901",
			Run:          checkPWAServable,
		},

		// --- Row 74: vnproxctl -------------------------------------------------
		{
			ID:           "cli.daemon_independent_commands",
			MatrixRow:    74,
			Area:         "vnproxctl operator CLI",
			Suite:        SuiteHardware,
			MinNodes:     1,
			Precondition: "a real vnprox install on a real PVE node, with vnproxctl on PATH and the packaged config in place",
			Run:          checkCLIDaemonIndependent,
		},
	}
}

// MutatorProbe is the write half of the daemon's API, and it is a separate
// interface from DaemonProbe for one structural reason: the CLI leaves
// Deps.Mutator nil unless --i-understand was passed.
//
// That makes "a hardware-suite check cannot change anything" a property of
// how the run is wired rather than a rule the checks are trusted to follow.
// A check that wants to mutate and finds a nil Mutator skips, naming the
// flag; there is no code path by which it mutates anyway.
type MutatorProbe interface {
	Post(ctx context.Context, path string, body any) (status int, resp []byte, err error)
}

// UnknownCheckError is returned by Run when --only names an ID that is not in
// the registry (AC6). It names every unknown ID and offers the nearest
// registered ones, because the failure mode it exists to prevent is a run
// that silently selects nothing and exits 0.
type UnknownCheckError struct {
	Unknown []string
	Known   []string
}

func (e *UnknownCheckError) Error() string {
	suggestions := make([]string, 0, len(e.Unknown))
	for _, u := range e.Unknown {
		if near := nearestIDs(u, e.Known); len(near) > 0 {
			suggestions = append(suggestions, fmt.Sprintf("%q (did you mean %s?)", u, strings.Join(quoteAll(near), ", ")))
			continue
		}
		suggestions = append(suggestions, fmt.Sprintf("%q", u))
	}
	return fmt.Sprintf("unknown check %s — run `vnproxctl verify --list` for the %d registered ids",
		strings.Join(suggestions, "; "), len(e.Known))
}

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return out
}

// nearestIDs returns registered ids sharing the unknown id's prefix segment,
// which covers the overwhelmingly common typo: the right family, the wrong
// tail.
func nearestIDs(unknown string, known []string) []string {
	family := unknown
	if i := strings.IndexByte(family, '.'); i > 0 {
		family = family[:i]
	}
	var out []string
	for _, k := range known {
		if strings.HasPrefix(k, family+".") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	const maxSuggestions = 4
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// ValidateRegistry enforces the properties every check must have before it is
// allowed to contribute a verdict to a signed artifact.
//
// It runs as a test (TestRegistryIsWellFormed) and again inside Run, so a
// registry defect cannot reach a report even if someone adds a check in a
// build where the tests were not run.
func ValidateRegistry(checks []Check) error {
	if len(checks) == 0 {
		return fmt.Errorf("verify: the check registry is empty")
	}
	seen := make(map[string]bool, len(checks))
	var problems []string
	for _, c := range checks {
		switch {
		case strings.TrimSpace(c.ID) == "":
			problems = append(problems, fmt.Sprintf("a check on matrix row %d has no id", c.MatrixRow))
			continue
		case seen[c.ID]:
			problems = append(problems, fmt.Sprintf("check %q is registered twice", c.ID))
			continue
		}
		seen[c.ID] = true

		if !strings.Contains(c.ID, ".") {
			problems = append(problems, fmt.Sprintf("check %q must be named <family>.<behaviour>", c.ID))
		}
		if c.MatrixRow <= 0 {
			problems = append(problems, fmt.Sprintf("check %q names no status-matrix.md row", c.ID))
		}
		if strings.TrimSpace(c.Area) == "" {
			problems = append(problems, fmt.Sprintf("check %q names no feature area", c.ID))
		}
		if !ValidSuite(c.Suite) {
			problems = append(problems, fmt.Sprintf("check %q is in unknown suite %q", c.ID, c.Suite))
		}
		if c.MinNodes < 1 {
			problems = append(problems, fmt.Sprintf("check %q requires %d nodes; every check needs at least one", c.ID, c.MinNodes))
		}
		if strings.TrimSpace(c.Precondition) == "" {
			// AC7. A check with no stated precondition produces a skip nobody
			// can act on, which is the same as no check at all.
			problems = append(problems, fmt.Sprintf("check %q states no hardware precondition (AC7)", c.ID))
		}
		if c.Run == nil {
			problems = append(problems, fmt.Sprintf("check %q has no Run function", c.ID))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("verify: malformed check registry:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// CheckIDs returns every registered id, sorted.
func CheckIDs(checks []Check) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.ID)
	}
	sort.Strings(out)
	return out
}
