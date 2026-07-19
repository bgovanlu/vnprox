package drift

import (
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
)

// Check family identifiers (docs/features/topology.md §6). These are the
// wire values of Finding.Check.
const (
	CheckBridgeDivergence      = "bridge_divergence"
	CheckMTUConsistency        = "mtu_consistency"
	CheckSDNRealization        = "sdn_realization"
	CheckPendingInterfaces     = "pending_interfaces"
	CheckFileRuntimeDivergence = "file_runtime_divergence"
	// CheckVFSpoofcheckMismatch (T-1506) is the standing-drift half of
	// vf_spoofcheck_mismatch: an already-diverged live VF (host-netlink
	// observed) whose VLAN/spoof-check setting no longer matches its PF's
	// bridge's own VLAN-awareness/VID-set policy — see sriov.go. The
	// identical comparison also runs at changeset-validate time, for a
	// *staged* vf.provision op, in internal/change/validate_referential.go.
	CheckVFSpoofcheckMismatch = "vf_spoofcheck_mismatch"
)

// Severity mirrors docs/api.md's changeset finding vocabulary
// ("error"|"warning"|"info") so the frontend can reuse the same severity
// styling for drift findings (the same reuse internal/topology.VlanFinding
// already does).
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Finding is one drift check result. The four lower-cased fields (check,
// severity, nodes, detail) are docs/api.md's documented `GET /drift` shape
// (`[{check, severity, nodes, detail}]`); id/refs/fixable are additive
// T-305 fields (docs/development.md's "definition of done" #4 — documented
// in docs/api.md alongside this task's other route additions).
//
// ID is a stable, deterministic key derived from the check name plus the
// sorted set of affected Refs (or, for checks with no ref-scoped entity,
// the sorted set of affected node names) — never randomly generated or
// time-based — so re-running the same checks against an unchanged snapshot
// reproduces byte-identical IDs every cycle (T-305 acceptance criterion 5:
// "stable-key dedup ... across repeated cycles on unchanged state").
type Finding struct {
	ID       string `json:"id"`
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
	fixTitle string
	Nodes    []string `json:"nodes"`
	Refs     []string `json:"refs,omitempty"`
	fixOps   []change.Op
	Fixable  bool `json:"fixable"`
}

// sortedUnique returns a sorted copy of ss with duplicates and empty
// strings removed.
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

// newFinding builds a Finding with a stable ID derived from check plus refs
// (preferred) or, when refs is empty, nodes. nodes and refs are both
// canonicalized (sorted, deduped) so field order never affects the ID or
// the rendered output.
func newFinding(check, severity, detail string, nodes, refs []string) Finding {
	nodes = sortedUnique(nodes)
	refs = sortedUnique(refs)
	keyParts := refs
	if len(keyParts) == 0 {
		keyParts = nodes
	}
	return Finding{
		ID:       check + "|" + strings.Join(keyParts, ","),
		Check:    check,
		Severity: severity,
		Detail:   detail,
		Nodes:    nodes,
		Refs:     refs,
	}
}

// withFix attaches a computable fix to f, returning the updated copy.
func (f Finding) withFix(title string, ops []change.Op) Finding {
	f.Fixable = true
	f.fixTitle = title
	f.fixOps = ops
	return f
}
