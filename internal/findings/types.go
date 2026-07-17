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
