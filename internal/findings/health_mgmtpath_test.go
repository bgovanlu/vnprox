package findings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

var errBoom = errors.New("boom")

// fakeMgmtProvider is a minimal findings.MgmtProvider stand-in — this
// check's actual path resolution is internal/topology's job (covered by
// that package's own golden tests), so here we only need to prove the
// check correctly turns a pre-resolved MgmtStatus into findings.
type fakeMgmtProvider struct {
	err    error
	status change.MgmtStatus
}

func (f fakeMgmtProvider) MgmtStatus() (change.MgmtStatus, error) { return f.status, f.err }

func mgmtRef(node, id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: id}
}

// TestMgmtSinglePath_NotRedundant_Fires: AC3's single-node case — exactly
// one finding, naming the node and the carrier ref.
func TestMgmtSinglePath_NotRedundant_Fires(t *testing.T) {
	status := change.MgmtStatus{
		Source: "detected",
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{
				Ref:       mgmtRef("pve1", "vmbr0"),
				Roles:     []topology.MgmtRole{topology.MgmtRoleMgmt},
				Path:      []inventory.Ref{{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}},
				Redundant: false,
			}},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{status: status}})

	found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath)
	if len(found) != 1 {
		t.Fatalf("got %d mgmt_single_path findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Severity != findings.SeverityWarning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
	if f.DocsLink == "" {
		t.Error("mgmt_single_path must carry a DocsLink")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}
	if len(f.Refs) != 1 || f.Refs[0] != "bridge:pve1:vmbr0" {
		t.Errorf("Refs = %v, want [bridge:pve1:vmbr0]", f.Refs)
	}
	if !strings.Contains(f.Detail, "pve1") || !strings.Contains(f.Detail, "vmbr0") {
		t.Errorf("detail = %q, want mention of pve1/vmbr0", f.Detail)
	}
}

// TestMgmtSinglePath_Redundant_NoFinding: AC3's three-node-vlan case — a
// redundant path never fires.
func TestMgmtSinglePath_Redundant_NoFinding(t *testing.T) {
	status := change.MgmtStatus{
		Source: "detected",
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{
				Ref:       mgmtRef("pve1", "vmbr0"),
				Roles:     []topology.MgmtRole{topology.MgmtRoleMgmt},
				Path:      []inventory.Ref{{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}},
				Redundant: true,
			}},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{status: status}})
	if found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath); len(found) != 0 {
		t.Fatalf("redundant path produced a finding: %+v", found)
	}
}

// TestMgmtSinglePath_StableIDAcrossPolls: the finding's id must not change
// between polls of unchanged state (AC3: "stable id across polls") — this
// check has no hysteresis state, so this also proves it isn't accidentally
// order- or timing-dependent.
func TestMgmtSinglePath_StableIDAcrossPolls(t *testing.T) {
	status := change.MgmtStatus{
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{Ref: mgmtRef("pve1", "vmbr0"), Roles: []topology.MgmtRole{topology.MgmtRoleMgmt}, Redundant: false}},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{status: status}})

	first := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath)
	second := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath)
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("id not stable across polls: %+v vs %+v", first, second)
	}
}

// TestMgmtSinglePath_CorosyncOnly_NoFinding: a ref carrying only the
// corosync role (never mgmt) never fires this check — losing corosync-link
// redundancy isn't "the node becomes unreachable".
func TestMgmtSinglePath_CorosyncOnly_NoFinding(t *testing.T) {
	status := change.MgmtStatus{
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{Ref: mgmtRef("pve1", "vmbr1"), Roles: []topology.MgmtRole{topology.MgmtRoleCorosync}, Redundant: false}},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{status: status}})
	if found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath); len(found) != 0 {
		t.Fatalf("corosync-only non-redundant ref produced a finding: %+v", found)
	}
}

// TestMgmtSinglePath_NilProvider_NoFindings: not wired -> quietly absent.
func TestMgmtSinglePath_NilProvider_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1")})
	if found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath); len(found) != 0 {
		t.Fatalf("nil Mgmt provider produced findings: %+v", found)
	}
}

// TestMgmtSinglePath_ProviderError_NoFindings: a computation error degrades
// to no findings rather than panicking/erroring the whole stream.
func TestMgmtSinglePath_ProviderError_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{err: errBoom}})
	if found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath); len(found) != 0 {
		t.Fatalf("provider error produced findings: %+v", found)
	}
}

// Phase 36: this producer declares its own remedy, and the frontend
// resolves it by `action` (web/src/findings/remediation.ts) rather than by
// testing `check === "mgmt_single_path"` in a component. If the declaration
// drifts from what the resolver expects, the button silently stops
// appearing and the finding still renders perfectly — so it is asserted
// here rather than left to an e2e to notice.
func TestMgmtSinglePath_DeclaresItsRemedy(t *testing.T) {
	status := change.MgmtStatus{
		Source: "detected",
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{
				Ref:       mgmtRef("pve1", "vmbr0"),
				Roles:     []topology.MgmtRole{topology.MgmtRoleMgmt},
				Path:      []inventory.Ref{{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}},
				Redundant: false,
			}},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Mgmt: fakeMgmtProvider{status: status}})

	found := findByCheck(t, eng.Findings(), findings.CheckMgmtSinglePath)
	if len(found) != 1 {
		t.Fatalf("got %d mgmt_single_path findings, want 1", len(found))
	}
	r := found[0].Remedy
	if r == nil {
		t.Fatal("finding carries no remedy — the redundancy wizard is unreachable from the findings stream")
	}
	if r.Action != findings.RemedyActionMgmtRedundancy {
		t.Errorf("action = %q, want %q", r.Action, findings.RemedyActionMgmtRedundancy)
	}
	// Navigate, not operational: choosing a second interface and its
	// addressing is a human decision, and this check cannot compute it.
	if r.Kind != findings.RemedyNavigate {
		t.Errorf("kind = %q, want %q", r.Kind, findings.RemedyNavigate)
	}
	// The wizard cannot open without a node; remediation.ts resolves a
	// remedy with no node to no button at all.
	if got := r.Params["node"]; got != "pve1" {
		t.Errorf("remedy node = %q, want pve1", got)
	}
	// Still detection-only as far as the change engine is concerned — a
	// remedy is not a computed changeset, and this finding must never start
	// claiming it has one.
	if found[0].Fixable {
		t.Error("mgmt_single_path became Fixable; Phase 36 must not add Tier 1 fixes")
	}
}
