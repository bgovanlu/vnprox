package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Scope names — the string form of internal/auth.Cap values a token's scopes
// are drawn from. Kept as local constants (rather than importing internal/auth)
// so this package stays a small, dependency-light leaf; the strings are pinned
// equal to auth's own by TestScopeNamesMatchAuthVocabulary in the auth-aware
// test wiring / by docs/api.md's Tokens table. `automation` is the connection
// gate (a session can't open without it — see Server.Authenticate); the others
// gate individual tools.
const (
	scopeNetRead    = "netRead"
	scopeNetWrite   = "netWrite"
	scopeAutomation = "automation"
)

// Tool names — the complete, fixed allowlist. This list is the surface's
// security boundary: exactly these thirteen tools exist, and NONE of them names
// or reaches an apply/confirm/rollback/discard verb. Adding a tool here whose
// name matches forbiddenToolSubstrings panics at init (validateRegistry), and
// TestRegistryIsStageOnlyAllowlist pins the exact set.
//
// The `changesets.stage.*` family (T-2705) is the typed staging surface: each
// one turns a small, schema-described request into exactly ONE op in a DRAFT
// changeset and returns that draft's id. They are mutating in the sense that
// they write an app-owned draft row — and in no other sense: they cannot touch
// the network, because staging a draft is the only thing the change-engine seam
// this package holds can do (see ChangesetStager in server.go and the
// compile-time assertion in stageonly.go).
const (
	ToolTopologyGet        = "topology.get"
	ToolFindingsList       = "findings.list"
	ToolFlowsQuery         = "flows.query"
	ToolIPAMSubnetsList    = "ipam.subnets.list"
	ToolSimulatePath       = "simulate.path"
	ToolDiagnoseRun        = "diagnose.run"
	ToolChangesetsDiff     = "changesets.diff"
	ToolChangesetsCreate   = "changesets.create"
	ToolChangesetsValidate = "changesets.validate"

	ToolStageBridge = "changesets.stage.bridge"
	ToolStageIface  = "changesets.stage.iface"
	ToolStageFwRule = "changesets.stage.fwrule"
	ToolStageIPAM   = "changesets.stage.ipam"
)

// stagingTools is the T-2705 typed-staging family, in registration order. It
// exists so tests (and docs generators) can enumerate exactly the tools that
// write, without re-deriving the set from a name prefix.
var stagingTools = []string{ToolStageBridge, ToolStageIface, ToolStageFwRule, ToolStageIPAM}

// forbiddenToolSubstrings are the mutating verbs no MCP tool name may ever
// contain. validateRegistry enforces this at init and
// TestNoMutatingToolByName re-asserts it — a structural bar against a future
// edit that would (say) add a `changesets.apply` tool. Matching is
// case-insensitive and substring-based so `apply`, `Apply`, `confirmChange`,
// etc. are all caught.
//
// T-2705 widened the original four (apply/confirm/rollback/discard) to cover
// the approve and destructive verbs too: an AI operator may open a draft, but
// approving one, deleting one, or destroying an entity are all human decisions
// with their own authenticated surfaces. The list is checked against every
// existing tool name by TestNoMutatingToolByName and
// TestNoApplyConfirmOrDeleteToolName (AC6).
var forbiddenToolSubstrings = []string{
	"apply", "confirm", "rollback", "discard",
	"approve", "delete", "destroy", "remove", "revert", "commit", "execute",
}

// ToolSpec is the static, dependency-free description of one tool: its name,
// the scope a token must hold for the tool to be exposed to its session, a
// human description, and the tool's input JSON-schema. Handlers live on the
// Server (they need its wired dependencies); a ToolSpec carries no behaviour,
// only identity and gating, so the allowlist can be enumerated and asserted in
// a pure test with no server at all (AC1).
type ToolSpec struct {
	Name          string
	RequiredScope string
	Description   string
	InputSchema   json.RawMessage
}

// toolSpecs is THE allowlist. Order is stable (it is the order tools/list
// reports and the order the enumeration test pins). RequiredScope mirrors each
// tool's HTTP counterpart's capability gate (docs/api.md): the read/simulate/
// diagnose surfaces and the read-only changeset diff are netRead; staging a
// draft (create) and re-validating one (validate, which moves draft<->validated
// state) are netWrite — so a {netRead, automation} token can never reach
// changesets.create (AC2).
var toolSpecs = []ToolSpec{
	{
		Name:          ToolTopologyGet,
		RequiredScope: scopeNetRead,
		Description:   "Return the current cluster network topology projection (nodes, edges, layers). Read-only; wraps GET /topology.",
		InputSchema:   schemaObject(nil, nil),
	},
	{
		Name:          ToolFindingsList,
		RequiredScope: scopeNetRead,
		Description:   "List the current open findings (drift, LLDP, IPAM, health, anomalies). Read-only; wraps GET /findings.",
		InputSchema:   schemaObject(nil, nil),
	},
	{
		Name:          ToolFlowsQuery,
		RequiredScope: scopeNetRead,
		Description:   "Query observed network flows with optional guest/subnet/source/vlan/port/proto/time filters. Read-only; wraps GET /flows.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"guest":  schemaString("filter to a guest ref"),
			"subnet": schemaString("filter to a CIDR"),
			"source": schemaString("filter to a source (sflow|netflow|ipfix|conntrack|ebpf)"),
			"vlan":   schemaInt("filter to a VLAN id"),
			"port":   schemaInt("filter to a destination port"),
			"proto":  schemaInt("filter to an IP protocol number"),
			"limit":  schemaInt("max rows to return (default 100, max 500)"),
		}, nil),
	},
	{
		Name:          ToolIPAMSubnetsList,
		RequiredScope: scopeNetRead,
		Description:   "List known subnets with utilization and conflict data. Read-only; wraps GET /ipam/subnets.",
		InputSchema:   schemaObject(nil, nil),
	},
	{
		Name:          ToolSimulatePath,
		RequiredScope: scopeNetRead,
		Description:   "Statically simulate a packet path (firewall + routing) between two endpoints. Read-only; wraps POST /simulate/path.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"src":    schemaEndpoint("source endpoint"),
			"dst":    schemaEndpoint("destination endpoint"),
			"proto":  schemaString("tcp|udp|icmp (optional)"),
			"port":   schemaInt("destination port (optional)"),
			"family": schemaString("ipv4|ipv6 (optional, defaults ipv4)"),
		}, []string{"src", "dst"}),
	},
	{
		Name:          ToolDiagnoseRun,
		RequiredScope: scopeNetRead,
		Description:   "Run the guided diagnosis ladder against a target ref (config-check, live-probe, guest-interior, conntrack). Read-only and advisory; never escalates to packet capture over MCP; wraps POST /diagnose. Never auto-remediates.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"targetRef": schemaString("inventory ref to diagnose (kind:node:id)"),
		}, []string{"targetRef"}),
	},
	{
		Name:          ToolChangesetsDiff,
		RequiredScope: scopeNetRead,
		Description:   "Render the diff for a staged changeset. Read-only; wraps GET /changesets/{id}/diff.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"id": schemaString("changeset id"),
		}, []string{"id"}),
	},
	{
		Name:          ToolChangesetsCreate,
		RequiredScope: scopeNetWrite,
		Description:   "Stage a new DRAFT changeset (an ordered list of typed ops). Creates a draft only — it is never applied by this tool; a human still reviews and applies it through the change engine. Labelled origin=mcp in the audit trail. Wraps POST /changesets.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"title": schemaString("human title for the draft"),
			"ops":   schemaArray("the changeset's typed ops"),
		}, []string{"ops"}),
	},
	{
		Name:          ToolChangesetsValidate,
		RequiredScope: scopeNetWrite,
		Description:   "Re-run validation on a staged changeset and return its findings. Wraps POST /changesets/{id}/validate. Does not apply.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"id": schemaString("changeset id"),
		}, []string{"id"}),
	},
	// --- T-2705 typed staging tools ------------------------------------
	//
	// Each stages exactly one op. `changesetId` is optional: absent, the tool
	// opens a NEW draft; present, it appends the op to that already-open
	// MCP-staged draft, so a multi-step change is one reviewable changeset
	// rather than four. Either way the result is a draft a human still
	// reviews and applies.
	{
		Name:          ToolStageBridge,
		RequiredScope: scopeNetWrite,
		Description: "Stage a bridge.create op in a DRAFT changeset and return the draft's id. " +
			"Creates a draft only; a human still reviews and applies it through the change engine. " +
			"Every op is checked against the cluster's policy rules before anything is staged — a denied op names the rule that refused it and stages nothing.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"targetRef":   schemaString("the new bridge's ref, 'bridge:<node>:<name>' (Linux) or 'ovs-bridge:<node>:<name>' (OVS)"),
			"addresses":   schemaStringArray("CIDR addresses to configure on the bridge"),
			"gateway":     schemaString("default gateway for the bridge (optional)"),
			"ports":       schemaStringArray("names of physical NICs/bonds to enslave (optional)"),
			"mtu":         schemaInt("MTU (optional)"),
			"vlanAware":   schemaBool("make the bridge VLAN-aware (optional)"),
			"stp":         schemaBool("enable STP (optional)"),
			"comments":    schemaString("interfaces(5) comment (optional)"),
			"title":       schemaString("title for the draft (optional)"),
			"changesetId": schemaString("append to this already-open MCP-staged draft instead of opening a new one (optional)"),
		}, []string{"targetRef"}),
	},
	{
		Name:          ToolStageIface,
		RequiredScope: scopeNetWrite,
		Description: "Stage an iface.update op (MTU, addresses, gateway, autostart, comments) in a DRAFT changeset and return the draft's id. " +
			"Creates a draft only; a human still reviews and applies it. Policy-checked at stage time.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"targetRef":     schemaString("the interface to edit, 'kind:node:id' (e.g. 'physnic:pve1:eno1', 'bridge:pve1:vmbr0')"),
			"mtu":           schemaInt("new MTU (optional)"),
			"addresses":     schemaStringArray("replacement CIDR address list (optional)"),
			"gateway":       schemaString("new default gateway (optional)"),
			"autostart":     schemaBool("bring the interface up at boot (optional)"),
			"comments":      schemaString("interfaces(5) comment (optional)"),
			"removeAddress": schemaBool("clear the stanza's address option (optional; ignored when addresses is set)"),
			"removeGateway": schemaBool("clear the stanza's gateway option (optional; ignored when gateway is set)"),
			"title":         schemaString("title for the draft (optional)"),
			"changesetId":   schemaString("append to this already-open MCP-staged draft instead of opening a new one (optional)"),
		}, []string{"targetRef"}),
	},
	{
		Name:          ToolStageFwRule,
		RequiredScope: scopeNetWrite,
		Description: "Stage an fw.rule.create op in a DRAFT changeset and return the draft's id. " +
			"Creates a draft only; a human still reviews and applies it. Policy-checked at stage time.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"targetRef":   schemaString("the ruleset the rule joins, 'fw-ruleset:<node>:<id>' (cluster/node/guest scope)"),
			"direction":   schemaString("in|out"),
			"action":      schemaString("ACCEPT|DROP|REJECT (or a security-group name)"),
			"proto":       schemaString("IP protocol, e.g. tcp/udp/icmp (optional)"),
			"source":      schemaString("source address/CIDR/alias (optional)"),
			"dest":        schemaString("destination address/CIDR/alias (optional)"),
			"sport":       schemaString("source port or range (optional)"),
			"dport":       schemaString("destination port or range (optional)"),
			"iface":       schemaString("interface the rule is scoped to (optional)"),
			"macro":       schemaString("PVE firewall macro (optional)"),
			"log":         schemaString("log level (optional)"),
			"comment":     schemaString("rule comment (optional)"),
			"pos":         schemaInt("position to insert at within the ruleset (default 0, the top)"),
			"enabled":     schemaBool("whether the rule is enabled (default true)"),
			"title":       schemaString("title for the draft (optional)"),
			"changesetId": schemaString("append to this already-open MCP-staged draft instead of opening a new one (optional)"),
		}, []string{"targetRef", "direction", "action"}),
	},
	{
		Name:          ToolStageIPAM,
		RequiredScope: scopeNetWrite,
		Description: "Reserve an IPAM address by staging an ipam.alloc.create op in a DRAFT changeset, and return the draft's id. " +
			"Creates a draft only — the reservation does not exist until a human applies the changeset. Policy-checked at stage time.",
		InputSchema: schemaObject(map[string]json.RawMessage{
			"targetRef":   schemaString("the parent SDN subnet, 'sdn-subnet::<cidr>'"),
			"cidr":        schemaString("the address to reserve, typically a /32 or /128 host route"),
			"hostname":    schemaString("hostname for the reservation (optional)"),
			"mac":         schemaString("MAC address for the reservation (optional)"),
			"comment":     schemaString("comment for the reservation (optional)"),
			"title":       schemaString("title for the draft (optional)"),
			"changesetId": schemaString("append to this already-open MCP-staged draft instead of opening a new one (optional)"),
		}, []string{"targetRef", "cidr"}),
	},
}

// Tools returns a copy of the static tool allowlist, in registration order.
// Callers (tests, docs generators) get the full set regardless of any token's
// scopes; per-session filtering is Session.exposedTools' job.
func Tools() []ToolSpec {
	out := make([]ToolSpec, len(toolSpecs))
	copy(out, toolSpecs)
	return out
}

// toolByName returns the spec for name, or ok=false.
func toolByName(name string) (ToolSpec, bool) {
	for _, t := range toolSpecs {
		if t.Name == name {
			return t, true
		}
	}
	return ToolSpec{}, false
}

// validateRegistry enforces the stage-only invariant on the static allowlist
// at package load: every tool name must be non-empty, unique, carry a
// recognized RequiredScope, and — the security bar — contain none of
// forbiddenToolSubstrings. A violation panics, so a build that ships an
// apply-shaped tool cannot start at all.
func validateRegistry() {
	seen := make(map[string]bool, len(toolSpecs))
	for _, t := range toolSpecs {
		if t.Name == "" {
			panic("mcp: tool with empty name in registry")
		}
		if seen[t.Name] {
			panic(fmt.Sprintf("mcp: duplicate tool name %q in registry", t.Name))
		}
		seen[t.Name] = true
		lower := strings.ToLower(t.Name)
		for _, bad := range forbiddenToolSubstrings {
			if strings.Contains(lower, bad) {
				panic(fmt.Sprintf("mcp: tool %q names a forbidden mutating verb %q — the MCP surface is stage-only", t.Name, bad))
			}
		}
		switch t.RequiredScope {
		case scopeNetRead, scopeNetWrite:
		default:
			panic(fmt.Sprintf("mcp: tool %q has unrecognized RequiredScope %q", t.Name, t.RequiredScope))
		}
	}
}

func init() { validateRegistry() }

// --- tiny JSON-schema builders ----------------------------------------------

func schemaObject(props map[string]json.RawMessage, required []string) json.RawMessage {
	obj := map[string]any{"type": "object"}
	if props == nil {
		props = map[string]json.RawMessage{}
	}
	obj["properties"] = props
	if len(required) > 0 {
		obj["required"] = required
	}
	b, _ := json.Marshal(obj)
	return b
}

func schemaString(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "string", "description": desc})
	return b
}

func schemaInt(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "integer", "description": desc})
	return b
}

func schemaBool(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "boolean", "description": desc})
	return b
}

func schemaStringArray(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}})
	return b
}

func schemaArray(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "object"}})
	return b
}

func schemaEndpoint(desc string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type":        "object",
		"description": desc,
		"properties": map[string]any{
			"kind":   map[string]any{"type": "string", "description": "guest-nic|ip|external"},
			"nicRef": map[string]any{"type": "string", "description": "guest-nic ref (when kind=guest-nic)"},
			"ip":     map[string]any{"type": "string", "description": "IP (when kind=ip)"},
		},
	})
	return b
}
