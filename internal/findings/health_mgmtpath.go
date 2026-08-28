// SPDX-License-Identifier: Apache-2.0

// health_mgmtpath.go implements docs/features/monitoring.md §5's
// "mgmt_single_path" health check (T-702): one finding per node whose
// management path — the physical interface(s) ultimately carrying its
// management IP or corosync links, resolved by internal/change's shared
// classification/path resolver (internal/topology.ResolveMgmtPaths) — has
// no redundancy (fewer than two link-up physical NICs in the path).
//
// Unlike every other health_*.go check in this package, this one is
// deliberately hysteresis-exempt (no debouncer): it is a structural
// property of the current network configuration (how many NICs are wired
// under this bridge/bond), not a threshold crossed by noisy live counters —
// there is nothing to debounce. It clears the instant the path becomes
// redundant (a bond gains a second up slave) and fires the instant it
// doesn't, exactly like docs/features/monitoring.md §5's other structural
// checks (e.g. "bond slave down" is itself hysteresis-based only because
// link flap is noisy; a *topology* fact like "this bridge has one NIC" is
// not).

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/topology"
)

const CheckMgmtSinglePath = "mgmt_single_path"

const mgmtSinglePathDocsLink = "docs/features/monitoring.md#5-health-checks"

// MgmtProvider is the seam checkMgmtSinglePath needs: T-702's shared
// management-path status computation (change.Service.MgmtStatus). No
// context parameter — Engine's own healthFindings cycle has no request
// context to thread through (mirrors DriftProvider/LLDPProvider/
// IPAMProvider's context-free shape); cmd/vnproxd's wiring adapts
// *change.Service accordingly.
type MgmtProvider interface {
	MgmtStatus() (change.MgmtStatus, error)
}

// checkMgmtSinglePath evaluates every node's resolved management-role refs
// (Roles containing RoleMgmt — corosync-only refs don't count: a corosync
// link losing redundancy is not "the node becomes unreachable", which is
// what this check is about) and flags one finding per (node, ref) whose
// path is not redundant. A nil provider (not wired) or a computation error
// yields no findings — detection-only, same "quietly absent" degradation
// docs/api.md documents for every other optional producer input.
func checkMgmtSinglePath(mgmtSvc MgmtProvider) []Finding {
	if mgmtSvc == nil {
		return nil
	}
	status, err := mgmtSvc.MgmtStatus()
	if err != nil {
		return nil
	}

	var out []Finding
	for node, paths := range status.Nodes {
		for _, p := range paths {
			if !hasMgmtRole(p.Roles) || p.Redundant {
				continue
			}
			carrier := p.Ref.String()
			detail := fmt.Sprintf(
				"node %s's management path carries no redundancy: %s is the only interface behind %s — if it fails, the node's management connectivity goes with it",
				node, pathDescription(p), carrier,
			)
			f := newHealthFinding(CheckMgmtSinglePath, SeverityWarning, detail, []string{node}, []string{carrier})
			f.DocsLink = mgmtSinglePathDocsLink
			// Phase 36: the remedy is a human decision — which second
			// interface, on which VLAN, with what addressing — so it is
			// Tier 3 navigation into T-703's redundancy wizard rather than
			// anything this check could compute. Declared here, by the
			// producer that knows what it means, instead of being inferred
			// from `check === "mgmt_single_path"` inside a React component
			// (which is how the frontend used to decide it, once per
			// surface).
			f.Remedy = &Remediation{
				Action: RemedyActionMgmtRedundancy,
				Kind:   RemedyNavigate,
				Label:  "Add a redundant path",
				Params: map[string]string{"node": node},
			}
			out = append(out, f)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// hasMgmtRole reports whether roles contains RoleMgmt (as opposed to
// corosync-only — a corosync link losing redundancy doesn't mean the node
// becomes unreachable, which is what this check is about).
func hasMgmtRole(roles []topology.MgmtRole) bool {
	for _, r := range roles {
		if r == topology.MgmtRoleMgmt {
			return true
		}
	}
	return false
}

// pathDescription renders a human-readable description of p's resolved
// physical path for the finding's plain-English detail text.
func pathDescription(p topology.MgmtPath) string {
	if len(p.Path) == 0 {
		return "no physical interface"
	}
	return "its single physical uplink"
}
