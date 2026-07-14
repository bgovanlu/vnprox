package change_test

// T-703's backend acceptance tests for the guided management-redundancy
// wizard's op output: the wizard is "interlock-clean by construction", so
// each flow's golden ops — the exact wire-shaped changesets
// web/src/mgmt/mgmtWizardOps.ts drafts (kept in lockstep by building them
// from the same JSON here, through change.Op's own strict decoder) — must
// validate with zero safety-class findings against the real fixture
// snapshots (AC1), while tampered variants that break the construction
// invariants must trip T-203's safety.protected_interface backstop (AC2).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const fixtureVlanMgmt = "../../testdata/clusters/vlan-mgmt.yaml"

// newTicketPVEClient builds a ticket-auth pve.Client against a running mock
// (pvemock does not implement API-token auth — see testdata/dev.toml).
func newTicketPVEClient(t *testing.T, apiURL string) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Config{APIURL: apiURL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return c
}

// fixtureSnapshot runs one full pvemock -> collect -> inventory.Graph poll
// cycle and returns the resulting snapshot — the same pipeline
// internal/topology's golden tests use, so these ops are validated against
// exactly what the daemon would see, not a hand-built approximation.
func fixtureSnapshot(t *testing.T, fixturePath string) inventory.Snapshot {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:   newTicketPVEClient(t, ts.URL),
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return graph.Snapshot()
}

// decodeOps decodes wire-shaped op JSON through change.Op's strict decoder —
// the same path a wizard-drafted POST /changesets body takes — so these
// tests pin the wire contract, not just Go struct literals.
func decodeOps(t *testing.T, opsJSON string) []change.Op {
	t.Helper()
	var ops []change.Op
	if err := json.Unmarshal([]byte(opsJSON), &ops); err != nil {
		t.Fatalf("decoding ops: %v", err)
	}
	return ops
}

// safetyFindings filters findings down to the safety class (the interlock
// backstop — AC1 requires exactly zero of these for every wizard flow).
func safetyFindings(findings []change.Finding) []change.Finding {
	var out []change.Finding
	for _, f := range findings {
		if strings.HasPrefix(f.Code, "safety.") {
			out = append(out, f)
		}
	}
	return out
}

func errorFindings(findings []change.Finding) []change.Finding {
	var out []change.Finding
	for _, f := range findings {
		if f.Severity == change.SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// validateWizardOps runs the full T-202/T-203 pipeline with the fixture's
// own detected protected set armed — the interlock stays the backstop
// behind the wizard, exactly as in production.
func validateWizardOps(t *testing.T, snap inventory.Snapshot, ops []change.Op) []change.Finding {
	t.Helper()
	protected := change.DetectProtected(snap, nil)
	if len(protected) == 0 {
		t.Fatal("fixture detected no protected interfaces — the backstop would not be armed")
	}
	return change.ValidateWithSafety(ops, snap, change.SafetyOptions{Protected: protected})
}

// flowABondUplinkOps is flow A's golden output on single-node
// (web/src/mgmt/mgmtWizardOps.ts buildBondUplinkOps): migrate the mgmt
// bridge's single uplink into a fresh two-slave bond, preserving the
// bridge's address by never touching it.
func flowABondUplinkOps(mode, extra string) string {
	return `[
	  {"op": "bridge.port.remove", "target": "bridge:pve1:vmbr0", "params": {"port": "eno1"}},
	  {"op": "bond.create", "target": "bond:pve1:bond0", "params": {"mode": "` + mode + `", "slaves": ["eno1", "eno2"], "miimon": 100` + extra + `}},
	  {"op": "bridge.port.add", "target": "bridge:pve1:vmbr0", "params": {"port": "bond0"}}
	]`
}

func TestMgmtWizard_FlowA_GoldenOpsValidateClean(t *testing.T) {
	snap := fixtureSnapshot(t, fixtureSingleNode)

	for _, tt := range []struct {
		name  string
		mode  string
		extra string
	}{
		{name: "active-backup (default when LLDP cannot confirm LACP)", mode: "active-backup"},
		{name: "802.3ad (explicit switch-side LACP required)", mode: "802.3ad", extra: `, "lacpRate": "fast", "xmitHashPolicy": "layer3+4"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ops := decodeOps(t, flowABondUplinkOps(tt.mode, tt.extra))
			findings := validateWizardOps(t, snap, ops)
			if sf := safetyFindings(findings); len(sf) != 0 {
				t.Fatalf("flow A (%s) tripped the safety interlock: %+v", tt.mode, sf)
			}
			if ef := errorFindings(findings); len(ef) != 0 {
				t.Fatalf("flow A (%s) has blocking errors: %+v", tt.mode, ef)
			}
		})
	}
}

func TestMgmtWizard_FlowB_GoldenOpsValidateClean(t *testing.T) {
	snap := fixtureSnapshot(t, fixtureThreeNode)

	t.Run("add spare slave eno3 to the mgmt-path bond", func(t *testing.T) {
		ops := decodeOps(t, `[
		  {"op": "bond.update", "target": "bond:pve1:bond0", "params": {"slaves": ["eno1", "eno2", "eno3"]}}
		]`)
		findings := validateWizardOps(t, snap, ops)
		if sf := safetyFindings(findings); len(sf) != 0 {
			t.Fatalf("flow B (add) tripped the safety interlock: %+v", sf)
		}
		if ef := errorFindings(findings); len(ef) != 0 {
			t.Fatalf("flow B (add) has blocking errors: %+v", ef)
		}
	})

	t.Run("replace slave eno2 with eno3", func(t *testing.T) {
		ops := decodeOps(t, `[
		  {"op": "bond.update", "target": "bond:pve1:bond0", "params": {"slaves": ["eno1", "eno3"]}}
		]`)
		findings := validateWizardOps(t, snap, ops)
		if sf := safetyFindings(findings); len(sf) != 0 {
			t.Fatalf("flow B (replace) tripped the safety interlock: %+v", sf)
		}
		if ef := errorFindings(findings); len(ef) != 0 {
			t.Fatalf("flow B (replace) has blocking errors: %+v", ef)
		}
	})

	// "Already redundant" is a UI statement (the wizard says so and offers
	// nothing destructive — web/src/mgmt tests); the backend property that
	// backs it is that three-node-vlan's resolved paths are redundant, which
	// internal/topology's mgmtpath golden tests already pin for pve1.
}

// flowCDedicatedVlanOps is flow C's golden output (buildDedicatedVlanOps):
// take the address and default route OFF the old carrier first, then create
// the dedicated VLAN sub-interface carrying the *same* address + gateway —
// order matters so the address-overlap referential check never sees both
// carriers at once, while the net effect preserves the management IP.
const flowCDedicatedVlanOpsVlanMgmt = `[
  {"op": "iface.update", "target": "vlan:pve1:vmbr0.30", "params": {"removeAddress": true, "removeGateway": true}},
  {"op": "vlan.create", "target": "vlan:pve1:vmbr0.40", "params": {"parent": "vmbr0", "vid": 40, "addresses": ["10.20.30.11/24"], "gateway": "10.20.30.1", "mtu": 1500}}
]`

func TestMgmtWizard_FlowC_GoldenOpsValidateClean(t *testing.T) {
	t.Run("vlan-mgmt fixture (VLAN carrier -> new VLAN carrier)", func(t *testing.T) {
		snap := fixtureSnapshot(t, fixtureVlanMgmt)
		ops := decodeOps(t, flowCDedicatedVlanOpsVlanMgmt)
		findings := validateWizardOps(t, snap, ops)
		if sf := safetyFindings(findings); len(sf) != 0 {
			t.Fatalf("flow C tripped the safety interlock: %+v", sf)
		}
		if ef := errorFindings(findings); len(ef) != 0 {
			t.Fatalf("flow C has blocking errors: %+v", ef)
		}
	})

	t.Run("single-node fixture (bridge carrier -> dedicated VLAN)", func(t *testing.T) {
		snap := fixtureSnapshot(t, fixtureSingleNode)
		ops := decodeOps(t, `[
		  {"op": "iface.update", "target": "bridge:pve1:vmbr0", "params": {"removeAddress": true, "removeGateway": true}},
		  {"op": "vlan.create", "target": "vlan:pve1:vmbr0.10", "params": {"parent": "vmbr0", "vid": 10, "addresses": ["192.168.1.10/24"], "gateway": "192.168.1.1", "mtu": 1500}}
		]`)
		findings := validateWizardOps(t, snap, ops)
		if sf := safetyFindings(findings); len(sf) != 0 {
			t.Fatalf("flow C (bridge carrier) tripped the safety interlock: %+v", sf)
		}
		if ef := errorFindings(findings); len(ef) != 0 {
			t.Fatalf("flow C (bridge carrier) has blocking errors: %+v", ef)
		}
	})
}

// TestMgmtWizard_TamperedOps_TripBackstop is AC2: mutate each flow's golden
// ops the way a buggy or bypassing client could, and prove T-203's
// net-effect interlock — armed unchanged behind the wizard — blocks the
// result with safety.protected_interface. (The complementary half of AC2 —
// that the wizard's own op builder cannot construct these shapes — is
// web/src/mgmt/mgmtWizardOps.test.ts's job.)
func TestMgmtWizard_TamperedOps_TripBackstop(t *testing.T) {
	t.Run("flow A without the bridge.port.add (mgmt bridge left port-less)", func(t *testing.T) {
		snap := fixtureSnapshot(t, fixtureSingleNode)
		ops := decodeOps(t, `[
		  {"op": "bridge.port.remove", "target": "bridge:pve1:vmbr0", "params": {"port": "eno1"}},
		  {"op": "bond.create", "target": "bond:pve1:bond0", "params": {"mode": "active-backup", "slaves": ["eno1", "eno2"], "miimon": 100}}
		]`)
		findings := validateWizardOps(t, snap, ops)
		if !hasFindingCode(findings, "safety.protected_interface", change.SeverityError) {
			t.Fatalf("tampered flow A did not trip safety.protected_interface: %+v", findings)
		}
	})

	t.Run("flow C re-addressed to a new IP (mgmt IP value not preserved)", func(t *testing.T) {
		snap := fixtureSnapshot(t, fixtureVlanMgmt)
		ops := decodeOps(t, `[
		  {"op": "iface.update", "target": "vlan:pve1:vmbr0.30", "params": {"removeAddress": true, "removeGateway": true}},
		  {"op": "vlan.create", "target": "vlan:pve1:vmbr0.40", "params": {"parent": "vmbr0", "vid": 40, "addresses": ["10.99.99.11/24"], "gateway": "10.99.99.1"}}
		]`)
		findings := validateWizardOps(t, snap, ops)
		if !hasFindingCode(findings, "safety.protected_interface", change.SeverityError) {
			t.Fatalf("tampered flow C did not trip safety.protected_interface: %+v", findings)
		}
	})
}

// TestMgmtWizard_FlowA_AutoRollback is T-703 AC4 at the achievable layer:
// a management-path changeset (flow A's golden ops) that applies but is
// never confirmed auto-rolls-back at the deadline to a byte-identical
// pre-state — the commit-confirm safety net the wizard's ceremony is built
// around. (The e2e can't drive this: the 180s mgmt confirm-window floor
// exceeds Playwright's 120s per-test timeout, and auto-rollback with mgmt
// actually down is on the hardware-validation list. This exercises the
// same daemon-side rollback timer via the harness's deterministic fake
// timer instead.)
func TestMgmtWizard_FlowA_AutoRollback(t *testing.T) {
	snap := fixtureSnapshot(t, fixtureSingleNode)
	h := newHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.Inventory = staticInventorySource{snap: snap}
	})
	ctx := context.Background()

	before := mustRead(t, h, "pve1")

	ops := decodeOps(t, flowABondUplinkOps("active-backup", ""))
	cs := h.mustCreate(t, "alice@pve", "Management redundancy: pve1", ops)
	if _, err := h.svc.Apply(ctx, cs.ID, "alice@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(h.agent.committedFile("pve1"), "bond0") {
		t.Fatal("bond0 not applied")
	}

	// The deadline elapses with no confirmation.
	h.timers.fireLatest(t)

	rolled := h.get(t, cs.ID)
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after deadline = %s, want rolled_back", rolled.Status)
	}
	if after := h.agent.committedFile("pve1"); after != before {
		t.Fatalf("pre-state not restored byte-identically after auto-rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func hasFindingCode(findings []change.Finding, code string, severity change.Severity) bool {
	for _, f := range findings {
		if f.Code == code && f.Severity == severity {
			return true
		}
	}
	return false
}
