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
	// Automation (T-1104) gates the WS "events" topic and the webhook
	// registration routes (POST/GET/DELETE /webhooks). Unlike every other
	// flag above, it is never derived from a PVE privilege —
	// DeriveCapabilities below never sets it, so a PVE-session-derived
	// Capabilities value (the map GET /auth/me reports) always has this
	// false. It exists purely as an api_tokens scope: minting (and then
	// presenting) a token whose scopes include "automation" is the only
	// way a request context ever carries Automation: true (see
	// internal/auth's bearer-token middleware). This is docs/api.md's "no
	// new privilege surface beyond that one addition" from T-1104's task
	// card — logging in with a browser session never grants automation
	// access on its own.
	Automation bool `json:"automation"`
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
	// mapping table below.
	CapAutomation Cap = "automation"
)

// AllCaps is every capability name recognized by Has/RequireCap, in the
// canonical order docs/api.md's GET /auth/me `caps` object documents them
// plus CapAutomation appended — internal/auth/tokens.go's scope validation
// (T-1104) iterates this rather than hardcoding a second copy of the
// vocabulary, so a future capability addition only needs updating in one
// place (this slice, Capabilities, and Has's switch).
var AllCaps = []Cap{
	CapNetRead, CapNetWrite, CapSDNRead, CapSDNWrite,
	CapFWRead, CapFWWrite, CapGuestNet, CapAudit, CapAutomation,
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
)

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
