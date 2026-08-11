// catalog.go declares the findings stream's own check vocabulary: every
// check name any producer can stamp on a Finding.
//
// WHY THIS EXISTS (T-2706). A compliance profile maps controls onto check
// names. Without a catalog, "which checks does no control map?" can only be
// answered from the checks that happen to be FIRING right now — so a check
// that has never fired on this cluster would be invisible, and a control
// could quietly lose a dimension of its evidence the day someone added a
// check and forgot to map it. That is exactly the silent degradation
// T-2706's acceptance criterion 6 forbids.
//
// It is a declared list rather than a derived one because check names are
// not all constants: several producers stamp a literal at the composition
// root (see the "literal-declared" block below). catalog_test.go closes the
// gap the other way — it parses the source of every package that declares a
// `Check… = "…"` constant and fails if any such constant is missing here, so
// a new check cannot be added to those packages without this list learning
// about it.
//
// KNOWN LIMIT, stated rather than discovered later: a check whose name is a
// bare literal in a package the guard does not parse can still be added
// without this list noticing. The guard covers the packages that own
// constants; the literal-declared entries below are hand-maintained and say
// so.

package findings

import "slices"

// allCheckNames is the catalog, grouped by declaring package for
// readability. Uniqueness is asserted by catalog_test.go; AllCheckNames
// returns it sorted, so a caller may rely on the order of what it gets back
// rather than on the order written here.
//
//nolint:gochecknoglobals // a read-only vocabulary table, the same shape internal/doctor.AllChecks already uses
var allCheckNames = []string{
	// --- internal/ipam (literal-declared: internal/ipam/merge.go's
	// Conflict.Type, adapted at the composition root by
	// cmd/vnproxd/findings.go) -------------------------------------------
	"allocated_dark",
	"duplicate_ip",
	"observed_unallocated",

	// --- literal-declared at the composition root -------------------------
	"approval_pending",                     // cmd/vnproxd/tenant.go
	"k8s_nodeport_exposed_without_fw_rule", // cmd/vnproxd/k8s.go
	"sim_divergence",                       // cmd/vnproxd/findings.go
	"webhook_unhealthy",                    // cmd/vnproxd/automation.go

	// --- constant-declared (guarded by catalog_test.go) -------------------
	CheckArpSpoofSuspected,
	CheckBondSlaveDown,
	"bridge_divergence", // internal/drift
	CheckBridgeNoCarrier,
	"capacity_ipam_forecast", // internal/capacity
	"capacity_link_forecast", // internal/capacity
	"cert_ca_mismatch",       // internal/certs
	"cert_expired",
	"cert_expiring",
	"cert_missing",
	"cert_not_chained",
	"cert_san_mismatch",
	"cert_unreadable",
	"cert_weak_key",
	CheckBreakGlass,
	CheckCorosyncLinkDegraded,
	CheckDualstackDrift,
	CheckErrorDropRate,
	CheckEvpnGwInconsistency,
	"file_runtime_divergence", // internal/drift
	CheckFwRuleUnused,
	"gitsync_commit_unsigned", // internal/gitsync
	"gitsync_divergence",
	"gitsync_signature_unverifiable",
	"gitsync_spec_unparseable",
	"gitsync_unreachable",
	CheckHAReplicationDegraded,
	CheckLACPPartnerMismatch,
	CheckMgmtSinglePath,
	"mtu_consistency", // internal/drift
	CheckNewPort,
	CheckNewSubnet,
	CheckOrphanVnet,
	CheckPathLatencyDegraded,
	CheckPathLoss,
	CheckPeerTrustDegraded,
	CheckPeerUnreachable,
	CheckPeerUntrusted,
	"pending_interfaces", // internal/drift
	CheckRogueDHCPServer,
	CheckScheduleMissed,
	"sdn_realization", // internal/drift
	CheckServiceDown,
	CheckServiceTrafficOnWrongNetwork,
	"spec_drift", // internal/drift
	CheckStalePendingInterfaces,
	CheckStoreNearCapacity,
	CheckSTPTopologyBurst,
	CheckTrunkUnusedVlans,
	CheckTunnelDownPeerUnreachable,
	CheckUnexpectedRA,
	CheckUnknownMacProtectedSegment,
	"vf_spoofcheck_mismatch",             // internal/drift
	"vlan_cross_check_missing_on_bridge", // internal/topology
	"vlan_cross_check_missing_on_switch", // internal/topology
	CheckVolumeSpike,
	CheckVxlanUnderlayMTU,
	CheckWanDegraded,
	CheckWgEndpointDrift,
	CheckWgHandshakeStale,
}

// AllCheckNames returns every check name the unified findings stream can
// emit, sorted and unique. The returned slice is a copy: a caller that sorts
// or appends to it cannot corrupt the catalog.
func AllCheckNames() []string {
	out := make([]string, len(allCheckNames))
	copy(out, allCheckNames)
	slices.Sort(out)
	return out
}
