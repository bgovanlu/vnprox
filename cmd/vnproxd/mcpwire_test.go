package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestMCPQueryFlows_FrozenPayloadFields is a regression guard against the
// exact shape of drift T-2002 almost shipped for internal/sim.RuleRef,
// caught here for real (T-3204: no test guarded this tool before): before
// this fix, mcpQueryFlows returned []store.FlowSample verbatim, whose bare
// Go field names (Node, SrcIP, ID, IngressIf, ...) matched neither
// docs/api.md's documented flow.Record shape nor GET /flows' own response
// for the identical underlying data. It now goes through the same
// api.FlowRecordJSON conversion GET /flows uses. This test drives a real
// insert through store.FlowSampleRepo and asserts the frozen `flows.query`
// payload's marshaled JSON carries docs/api.md's documented field set.
func TestMCPQueryFlows_FrozenPayloadFields(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "flows.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := store.NewFlowSampleRepo(db)
	if insertErr := repo.InsertBatch(ctx, []store.FlowSample{{
		Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.1.1.50", SrcRef: "bridge:pve1:vmbr0", DstRef: "bridge:pve2:vmbr0",
		Source: "netflow5", At: 1, Bytes: 150_000, Packets: 100, SrcPort: 51000, DstPort: 443, Proto: 6, VLAN: 100,
		IngressIf: 1, EgressIf: 2,
	}}); insertErr != nil {
		t.Fatalf("InsertBatch: %v", insertErr)
	}

	res, err := mcpQueryFlows(ctx, repo, nil, nil)
	if err != nil {
		t.Fatalf("mcpQueryFlows: %v", err)
	}
	payload, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var generic struct {
		NextCursor string           `json:"nextCursor"`
		Items      []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(payload, &generic); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(generic.Items) != 1 {
		t.Fatalf("items = %v, want one entry (payload: %s)", generic.Items, payload)
	}
	item := generic.Items[0]
	// docs/api.md's flow.Record shape: "{at, node, srcIp, dstIp, srcPort?,
	// dstPort?, proto, bytes, packets, vlan?, srcRef?, dstRef?,
	// ingressIfIndex?, egressIfIndex?, source, serviceClass?}".
	for _, field := range []string{"at", "node", "srcIp", "dstIp", "srcPort", "dstPort", "proto", "bytes", "packets", "vlan", "srcRef", "dstRef", "ingressIfIndex", "egressIfIndex", "source"} {
		if _, ok := item[field]; !ok {
			t.Errorf("flows.query item missing frozen field %q (payload: %s)", field, payload)
		}
	}
	// The Go-struct-verbatim shape this test guards against: none of these
	// bare Go field names may leak onto the wire.
	for _, leaked := range []string{"Node", "SrcIP", "DstIP", "ID", "IngressIf", "EgressIf"} {
		if _, ok := item[leaked]; ok {
			t.Errorf("flows.query item leaks store.FlowSample's bare Go field %q onto the wire (payload: %s)", leaked, payload)
		}
	}
}

// TestMCPPathConstantsAgree pins config.DefaultMCPPath equal to
// api.DefaultMCPPath, so the config docs and the router mount can never drift
// (the two packages don't import each other).
func TestMCPPathConstantsAgree(t *testing.T) {
	if config.DefaultMCPPath != api.DefaultMCPPath {
		t.Fatalf("MCP path constants disagree: config=%q api=%q", config.DefaultMCPPath, api.DefaultMCPPath)
	}
}

// TestSetupMCPBuildsServer is a wiring smoke test: setupMCP constructs a live
// MCP server from the daemon's real change engine + token/audit repos, and its
// HTTP handler is non-nil. Read seams are left nil here (they degrade to
// "not available" per-tool) — the point is that the security-critical staging +
// auth path wires cleanly.
func TestSetupMCPBuildsServer(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	changeSvc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Now:        func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	srv, err := setupMCP(api.Options{}, changeSvc, store.NewAPITokenRepo(db), store.NewAuditRepo(db),
		nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("setupMCP: %v", err)
	}
	if srv.HTTPHandler() == nil {
		t.Fatalf("setupMCP returned a server with a nil HTTP handler")
	}
}

// TestMCPSimulatePath_FrozenPayloadFields is a regression guard against the
// exact mistake T-2002 almost shipped (see planning/reports/T-2002.md):
// mcpSimulatePath returns sim.Result **verbatim** as the frozen
// `simulate.path` MCP tool's payload (docs/architecture.md §13.1, decision
// D10 — additive-only, no field ever removed/renamed without a version
// bump). A change to internal/sim's types that looks like a safe internal
// cleanup (no Go/TypeScript consumer left in this repo) can silently break
// an external MCP client reading the wire JSON, since that client's code
// never appears in this repo to grep for. This test drives a real
// guest-origin deny through mcpSimulatePath end to end and asserts the
// marshaled JSON still carries every documented field, including
// `blockingRule.rulesetRef` — the field this task originally removed and
// then had to restore.
func TestMCPSimulatePath_FrozenPayloadFields(t *testing.T) {
	g := inventory.NewGraph()
	bridge := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
		Name: "vmbr0", Virt: inventory.BridgeLinux, VlanAware: true, VlanAwareSet: true, Gateway: "10.0.0.1",
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{bridge})

	guests := []inventory.Entity{
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, VMID: 100, Name: "a", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, Key: "net0", TargetName: "vmbr0", Firewall: true},
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}, VMID: 101, Name: "b", Node: "pve1", Type: "qemu"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "101/net0"},
			Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "101"}, Key: "net0", TargetName: "vmbr0", Firewall: true},
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, guests)

	fwEnts := []inventory.Entity{
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster,
			Enabled: true, DefaultIn: "ACCEPT", DefaultOut: "ACCEPT"},
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}, Scope: inventory.FwScopeGuest, Enabled: true},
		// guest 101's own ruleset DROPs inbound tcp/22 — a guest-origin
		// deny, the case RulesetRef used to be left empty for.
		&inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/101"}, Scope: inventory.FwScopeGuest, Enabled: true,
			Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "22"}}},
	}
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, fwEnts)

	args, err := json.Marshal(map[string]any{
		"src":   map[string]any{"kind": "guest-nic", "nicRef": "guest-nic:pve1:100/net0"},
		"dst":   map[string]any{"kind": "guest-nic", "nicRef": "guest-nic:pve1:101/net0"},
		"proto": "tcp",
		"port":  22,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	res, err := mcpSimulatePath(g, args)
	if err != nil {
		t.Fatalf("mcpSimulatePath: %v", err)
	}

	payload, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(payload, &generic); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if generic["verdict"] != "deny" {
		t.Fatalf("verdict = %v, want deny (payload: %s)", generic["verdict"], payload)
	}
	for _, field := range []string{"verdict", "src", "dst", "hops", "caveats", "blockingRule"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("simulate.path payload missing frozen top-level field %q (payload: %s)", field, payload)
		}
	}
	blockingRule, ok := generic["blockingRule"].(map[string]any)
	if !ok {
		t.Fatalf("blockingRule is not an object: %v", generic["blockingRule"])
	}
	for _, field := range []string{"enforcementPoint", "rulesetRef", "origin", "direction", "action", "rule", "pos"} {
		if _, ok := blockingRule[field]; !ok {
			t.Errorf("simulate.path payload's blockingRule missing frozen field %q (payload: %s)", field, payload)
		}
	}
	if rulesetRef, _ := blockingRule["rulesetRef"].(string); rulesetRef == "" {
		t.Errorf("blockingRule.rulesetRef is empty for a guest-origin deny — the exact regression this test guards against")
	}
}
