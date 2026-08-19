package auth

import "github.com/bgovanlu/vnprox/internal/pve"

// Capabilities is the per-node capability flag set returned by GET
// /auth/me and enforced by RequireCap-gated routes, per docs/api.md's
// documented shape:
//
//	{caps: {netRead, netWrite, sdnRead, sdnWrite, fwRead, fwWrite, guestNet, audit}}
//
// This is vnprox's own "vnprox-enforced" authorization layer
// (docs/security.md "Authorization" §2) for the handful of host-level
// operations that bypass the PVE API entirely (reading LLDP, staging
// /etc/network/interfaces, ifreload, snapshot restore). It is a UX/safety
// net, not the primary enforcement: every PVE API write vnprox makes still
// goes out on the logged-in user's own PVE ticket, so PVE's own ACL engine
// is always the final word regardless of what these flags say.
type Capabilities struct {
	NetRead  bool `json:"netRead"`
	NetWrite bool `json:"netWrite"`
	SDNRead  bool `json:"sdnRead"`
	SDNWrite bool `json:"sdnWrite"`
	FWRead   bool `json:"fwRead"`
	FWWrite  bool `json:"fwWrite"`
	GuestNet bool `json:"guestNet"`
	Audit    bool `json:"audit"`
	// Automation (T-1104) is the READ half of the automation surface: it
	// gates the WS "events" topic and GET /webhooks (listing registrations).
	// Unlike every other flag above, it is never derived from a PVE
	// privilege — DeriveCapabilities below never sets it, so a
	// PVE-session-derived Capabilities value (the map GET /auth/me reports)
	// always has this false. It exists purely as an api_tokens scope:
	// minting (and then presenting) a token whose scopes include
	// "automation" is the only way a request context ever carries
	// Automation: true (see internal/auth's bearer-token middleware). This
	// is docs/api.md's "no new privilege surface beyond that one addition"
	// from T-1104's task card — logging in with a browser session never
	// grants automation access on its own.
	//
	// Split from a single "automation" flag by T-3003-followup-01
	// (2026-08-19): read_only (forceReadOnly below) must be able to keep
	// the read half — the events topic — available to a read-only consumer
	// while clearing the write half, AutomationWrite. Before the split, a
	// read_only deployment could not distinguish "watch events" from
	// "register a webhook that POSTs an outbound HTTP request", so it had
	// to either grant both or neither; docs/security.md's read_only
	// paragraph documents the split.
	Automation bool `json:"automation"`
	// AutomationWrite (T-3003-followup-01) is the WRITE half of the
	// automation surface split out of Automation above: it gates
	// POST /webhooks and DELETE /webhooks/{id} (registering/removing an
	// outbound delivery target the daemon will POST real HTTP requests to
	// — genuinely mutating, unlike the read half). Like Automation, it is
	// never derived from a PVE privilege — it exists purely as an
	// api_tokens scope. forceReadOnly clears this flag (and leaves
	// Automation set), so a read_only deployment's automation-scoped
	// tokens keep the events topic and GET /webhooks but lose the ability
	// to register or remove a webhook.
	AutomationWrite bool `json:"automationWrite"`
	// Capture (T-1301) gates the distributed packet-capture surface
	// (POST /captures + the /api/peer/capture/* routes it fans out to).
	// Unlike NetWrite/FWWrite it is deliberately NOT derived from Sys.Modify
	// alone: packet capture exposes raw payload bytes — a materially
	// stronger read than any config counter — so a session must hold BOTH
	// Sys.Modify AND Sys.Console (root-shell-equivalent on the node) for it
	// (DeriveCapabilities below). Holding netRead/netWrite alone therefore
	// never grants capture; docs/security.md's Authorization section
	// documents this pairing as at least as strict as netWrite's own gate.
	Capture bool `json:"capture"`
}

// Cap names a single capability flag by its JSON field name, for use with
// RequireCap and Capabilities.Has. Route registrations (T-106 topology, and
// eventually the change-engine routes) import these constants rather than
// spelling the strings out.
type Cap string

const (
	CapNetRead  Cap = "netRead"
	CapNetWrite Cap = "netWrite"
	CapSDNRead  Cap = "sdnRead"
	CapSDNWrite Cap = "sdnWrite"
	CapFWRead   Cap = "fwRead"
	CapFWWrite  Cap = "fwWrite"
	CapGuestNet Cap = "guestNet"
	CapAudit    Cap = "audit"
	// CapAutomation is T-1104's addition — see Capabilities.Automation's
	// doc comment for why it is not part of the PVE-privilege-derived
	// mapping table below. As of T-3003-followup-01 it is specifically the
	// READ half of the automation surface (see CapAutomationWrite).
	CapAutomation Cap = "automation"
	// CapAutomationWrite is T-3003-followup-01's write half of the
	// automation surface — see Capabilities.AutomationWrite's doc comment.
	CapAutomationWrite Cap = "automationWrite"
	// CapCapture is T-1301's dedicated packet-capture gate — see
	// Capabilities.Capture's doc comment for why it is a Sys.Modify +
	// Sys.Console pairing rather than a reuse of the netWrite mapping.
	CapCapture Cap = "capture"
)

// AllCaps is every capability name recognized by Has/RequireCap, in the
// canonical order docs/api.md's GET /auth/me `caps` object documents them
// plus CapAutomation/CapAutomationWrite appended — internal/auth/tokens.go's
// scope validation (T-1104) iterates this rather than hardcoding a second
// copy of the vocabulary, so a future capability addition only needs
// updating in one place (this slice, Capabilities, and Has's switch).
var AllCaps = []Cap{
	CapNetRead, CapNetWrite, CapSDNRead, CapSDNWrite,
	CapFWRead, CapFWWrite, CapGuestNet, CapAudit, CapAutomation, CapCapture,
	CapAutomationWrite,
}

// Has reports whether c grants the named capability. Unknown names return
// false rather than panicking, since a name only ever originates from this
// package's own Cap constants.
func (c Capabilities) Has(name Cap) bool {
	switch name {
	case CapNetRead:
		return c.NetRead
	case CapNetWrite:
		return c.NetWrite
	case CapSDNRead:
		return c.SDNRead
	case CapSDNWrite:
		return c.SDNWrite
	case CapFWRead:
		return c.FWRead
	case CapFWWrite:
		return c.FWWrite
	case CapGuestNet:
		return c.GuestNet
	case CapAudit:
		return c.Audit
	case CapAutomation:
		return c.Automation
	case CapAutomationWrite:
		return c.AutomationWrite
	case CapCapture:
		return c.Capture
	default:
		return false
	}
}

// The PVE privilege names this package's capability mapping understands.
// These mirror internal/pvemock's own Priv* constants (its documented
// subset of real PVE privileges vnprox maps to UI capability flags, per
// docs/architecture.md §6) — kept as independent constants here (rather
// than importing internal/pvemock, a test-only dependency) since caps.go is
// "the single source of truth" for the mapping per docs/security.md, and
// must hold even if internal/pvemock is absent (e.g. driven from real PVE).
const (
	privSysAudit    = "Sys.Audit"
	privSysModify   = "Sys.Modify"
	privSDNAudit    = "SDN.Audit"
	privSDNAllocate = "SDN.Allocate"
	privVMConfigNet = "VM.Config.Network"
	// privSysConsole is the second half of T-1301's capture pairing: PVE's
	// node-scope root-shell privilege. Requiring it on top of Sys.Modify is
	// what keeps `capture` strictly stronger than `netWrite` (Sys.Modify
	// alone) — the same access class needed to run tcpdump on the host by
	// hand.
	privSysConsole = "Sys.Console"
	// privVMAudit and privMappingAudit back DaemonTokenPrivileges only —
	// neither is ever consulted by DeriveCapabilities (guestNet comes from
	// VM.Config.Network, not VM.Audit; nothing in the capability mapping
	// reads Mapping.Audit at all), so they deliberately have no role in
	// RequiredPrivileges/TestRequiredPrivilegesCoversMapping's "operator
	// capability" contract. See DaemonTokenPrivileges' doc comment.
	privVMAudit      = "VM.Audit"
	privMappingAudit = "Mapping.Audit"
)

// RequiredPrivilege is one PVE privilege vnprox reads, paired with what is
// lost without it. Consumed by `vnproxctl doctor` (T-1904), whose job is to
// say "your token is missing X, so Y will not work" in the operator's words.
type RequiredPrivilege struct {
	// Name is the PVE privilege as PVE itself spells it ("Sys.Modify").
	Name string
	// Unlocks names, in operator terms, what holding it enables.
	Unlocks string
	// Optional marks a privilege whose absence degrades vnprox rather than
	// breaking it — a warn, not a fail.
	Optional bool
}

// RequiredPrivileges returns the privileges vnprox's capability mapping
// actually consults, in the order an operator should grant them.
//
// It is derived from the same constants DeriveCapabilities uses, on purpose:
// a diagnostic that checks a hand-maintained second list would eventually
// report on privileges the product no longer uses, or stay silent about ones
// it started using — the failure mode docs/security.md's "single source of
// truth" note exists to prevent. Adding a privilege to the mapping without
// adding it here fails TestRequiredPrivilegesCoversMapping.
func RequiredPrivileges() []RequiredPrivilege {
	return []RequiredPrivilege{
		{Name: privSysAudit, Unlocks: "reading node network config, firewall rules, and vnprox's own audit log — without it vnprox shows nothing"},
		{Name: privSysModify, Unlocks: "staging and applying node network and firewall changes — without it vnprox is read-only"},
		{Name: privSDNAudit, Unlocks: "reading SDN zones, VNets, and subnets"},
		{Name: privSDNAllocate, Unlocks: "creating and modifying SDN objects"},
		{Name: privVMConfigNet, Unlocks: "editing guest NICs"},
		{Name: privSysConsole, Unlocks: "packet capture, which additionally requires Sys.Modify (root-shell-equivalent access, deliberately)", Optional: true},
	}
}

// DaemonTokenPrivileges returns the privileges vnprox's OWN daemon-held PVE
// API token (vnprox@pve!daemon) is expected to hold — a DIFFERENT list from
// RequiredPrivileges, and consulted by a different caller for a different
// question.
//
// RequiredPrivileges answers "what would let an OPERATOR's own PVE ticket
// unlock every vnprox UI capability" (via DeriveCapabilities) — it
// necessarily includes Sys.Modify, SDN.Allocate, and VM.Config.Network,
// three write privileges. This list answers "what does the daemon's own
// configured token need" — and the daemon's token is deliberately
// read-only by design (docs/security.md's "Apply-time revert ticket": every
// write vnprox makes goes out on the *applying user's own* sealed PVE
// ticket, never the daemon's), so it is never granted those three, and
// never should be.
//
// Before this list existed, `vnproxctl doctor`'s pve_privileges check
// (internal/doctor/checks.go's checkPVEPrivileges) reused RequiredPrivileges
// for both purposes — which meant a token provisioned EXACTLY as documented
// (packaging/bin/vnprox-setup's read-only grant) permanently failed
// pve_privileges on every correctly-set-up install: confirmed on a real
// two-node PVE 9.2.10 cluster, byte-identical failures on both nodes
// (planning/reports/blocked-validation.md §2.4).
//
// This is also not simply "RequiredPrivileges minus the write entries":
// VM.Audit and Mapping.Audit are privileges the daemon's token genuinely
// needs (VM.Audit for guest NIC inventory reads; Mapping.Audit for GET
// /cluster/notifications/targets, which real hardware validation — T-608 —
// found PVE gates on Mapping.Audit specifically, not Sys.Audit like every
// other route this token calls) that RequiredPrivileges/DeriveCapabilities
// never consult at all, since neither backs a vnprox UI capability flag.
// So this is its own independently-declared list, kept equal to
// vnprox-setup's own VNPROX_PVE_PRIVS grant by construction rather than
// derived from RequiredPrivileges — see
// TestDaemonTokenPrivilegesMatchesSetupGrant, which pins the two together
// mechanically the same way TestRequiredPrivilegesCoversMapping pins
// RequiredPrivileges against caps.go's own mapping table.
func DaemonTokenPrivileges() []RequiredPrivilege {
	return []RequiredPrivilege{
		{Name: privSysAudit, Unlocks: "reading node network config, firewall rules, and vnprox's own audit log — without it vnprox's collectors see nothing"},
		{Name: privVMAudit, Unlocks: "reading guest (VM/LXC) NIC configuration for the topology view"},
		{Name: privSDNAudit, Unlocks: "reading SDN zones, VNets, and subnets"},
		{Name: privMappingAudit, Unlocks: "reading PVE notification targets, so findings can be delivered (GET /cluster/notifications/targets)"},
	}
}

// DeriveCapabilities maps one node's effective PVE privilege set to vnprox
// capability flags. This is docs/security.md's documented mapping table,
// the single source of truth (docs/security.md: "The mapping table lives
// in internal/auth/caps.go"):
//
//   - Sys.Audit on /nodes/{node}  -> netRead, fwRead, audit
//   - Sys.Modify on /nodes/{node} -> netWrite, fwWrite
//   - SDN.Audit                   -> sdnRead
//   - SDN.Allocate                -> sdnWrite
//   - VM.Config.Network           -> guestNet
//   - Sys.Modify AND Sys.Console  -> capture (T-1301)
//
// fwRead/fwWrite reuse Sys.Audit/Sys.Modify rather than a dedicated
// firewall privilege because that is exactly how PVE itself (and
// internal/pvemock, see its firewall.go: mountFirewallScope's
// cluster/node scopes are gated on PrivSysAudit/PrivSysModify) gates
// cluster- and node-scope firewall reads/writes; guest-scope firewall
// uses VM.Audit/VM.Config.Network instead, which is folded into guestNet
// here (vnprox does not expose a separate "guest firewall" flag — see
// this package's doc comment on that decision). audit reuses Sys.Audit:
// viewing vnprox's own audit log is itself an audit-level read.
func DeriveCapabilities(privs privilegeSet) Capabilities {
	return Capabilities{
		NetRead:  privs.has(privSysAudit),
		NetWrite: privs.has(privSysModify),
		SDNRead:  privs.has(privSDNAudit),
		SDNWrite: privs.has(privSDNAllocate),
		FWRead:   privs.has(privSysAudit),
		FWWrite:  privs.has(privSysModify),
		GuestNet: privs.has(privVMConfigNet),
		Audit:    privs.has(privSysAudit),
		Capture:  privs.has(privSysModify) && privs.has(privSysConsole),
	}
}

// privilegeSet is the resolved (path-flattened) set of PVE privileges
// effective for one capability-derivation scope (cluster-wide, or one
// node). It exists as its own type (rather than a bare map or []string) so
// DeriveCapabilities has one obvious calling convention regardless of
// whether the caller built it from a live pve.Permissions result
// (BuildCapabilities) or directly from a flat privilege-name list
// (newPrivilegeSet, as this package's mapping-table unit test does).
type privilegeSet map[string]bool

func (p privilegeSet) has(priv string) bool { return p != nil && (p["*"] || p[priv]) }

// newPrivilegeSet builds a privilegeSet from a flat list of privilege
// names, honoring PVE's "*" wildcard (full access, as internal/pvemock's
// UserSpec.HasPrivilege also does) exactly like BuildCapabilities does for
// a live GET /access/permissions result.
func newPrivilegeSet(privs []string) privilegeSet {
	out := make(privilegeSet, len(privs))
	for _, p := range privs {
		out[p] = true
	}
	return out
}

// BuildCapabilities derives one Capabilities per node from a live GET
// /access/permissions result (pve.Permissions) plus the cluster's node
// list. Privileges granted at "/" apply to every node and to the
// cluster-wide (SDN) scope; privileges granted at "/nodes/{node}" apply
// only to that node. This matches real PVE's ACL inheritance model closely
// enough for vnprox's own (secondary, UX-only) capability flags — the
// primary enforcement is always PVE's own ACL check on the user's ticket.
//
// If nodes is empty (e.g. the user's own ticket could not enumerate the
// cluster's node list — see internal/auth's login handler), the result has
// a single entry keyed by the empty string "" representing the cluster-wide
// (root-scope-only) capability set, so callers still get a usable, if
// less granular, answer instead of an empty map.
func BuildCapabilities(perms pve.Permissions, nodes []string) map[string]Capabilities {
	root := newPrivilegeSet(nil)
	for priv, granted := range perms["/"] {
		if granted {
			root[priv] = true
		}
	}

	if len(nodes) == 0 {
		return map[string]Capabilities{"": DeriveCapabilities(root)}
	}

	out := make(map[string]Capabilities, len(nodes))
	for _, node := range nodes {
		scoped := make(privilegeSet, len(root))
		for priv := range root {
			scoped[priv] = true
		}
		for priv, granted := range perms["/nodes/"+node] {
			if granted {
				scoped[priv] = true
			}
		}
		out[node] = DeriveCapabilities(scoped)
	}
	return out
}
