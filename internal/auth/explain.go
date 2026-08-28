// SPDX-License-Identifier: Apache-2.0

package auth

// explain.go is T-4105's "why can't I?" answer: given a capability a
// request was just denied on, name exactly which PVE privilege (and at
// which ACL path) would grant it — resolved through the *same*
// capability derivation RequireCap already consulted to deny the
// request (caps.go's DeriveCapabilities/BuildCapabilities), never a
// second copy of that table and never a fresh PVE round trip. D3 makes
// this fully determinable for an ordinary PVE-ticket session: every
// vnprox write goes out on the user's own ticket, so a denial has a
// precise, computable cause.
//
// Two session shapes break that determinism on purpose, and Explain says
// less for both rather than asserting something it cannot verify from
// already-derived data (AC2/AC3: read-only over BuildCapabilities'
// existing output, no re-derivation):
//
//   - An OIDC-authenticated session's Capabilities are
//     IntersectCaps(oidcBundle, pveDerivedCaps) (oidc_caps.go) — a false
//     flag can come from the OIDC group policy, the linked PVE account's
//     own ACLs, or both, and the intersection has already discarded
//     which. Naming a specific PVE privilege here would mean peeking at
//     the pre-intersection PVE-derived component — which would reveal
//     the caller's raw, uncapped PVE grant even when their org's OIDC
//     group policy exists specifically to withhold it. That is exactly
//     the shape of leak tenantMutationScope (internal/api/tenant.go)
//     refuses for a non-member's tenant: don't confirm a fact a policy
//     layer means to hide. So Explain does not name a privilege for an
//     OIDC session; it names the ambiguity instead.
//   - A bearer-token-authenticated request's Capabilities come from
//     CapabilitiesFromScopes (tokens.go) — the token's minted scopes,
//     not any PVE ACL lookup at all. Naming a PVE privilege here would
//     not leak anything (nothing is being withheld), but it would be
//     flatly wrong remediation advice: the fix is minting a
//     differently-scoped token, not asking a PVE admin for a privilege.
//   - A `[server] read_only = true` deployment (handlers.go's
//     forceReadOnly, docs/security.md's "observe-only until you trust
//     it" mode) zeroes netWrite/sdnWrite/fwWrite/guestNet/capture/
//     automationWrite on every session in place, at derivation time —
//     indistinguishable, by the time Explain runs, from that same flag
//     genuinely being absent from the caller's own PVE ACLs. Naming a
//     PVE privilege here risks telling an operator to chase a PVE ACL
//     grant they may already hold, when the actual and only fix is the
//     server's own `read_only` config.
type PrivilegeRequirement struct {
	// Privilege is the PVE privilege name (e.g. "Sys.Modify") that would
	// grant it.
	Privilege string `json:"privilege"`
	// Path is the PVE ACL path Privilege would need to be granted at for
	// the request's own scope: "/nodes/{node}" when the denied request
	// was scoped to one node, "/" (cluster-wide, covers every node)
	// otherwise.
	Path string `json:"path"`
	// Confirmed is true when the identity's own already-derived
	// Capabilities flags affirmatively show this exact privilege absent
	// (e.g. netWrite's Sys.Modify: netWrite IS exactly that privilege, so
	// netWrite==false always confirms it). It is false for a privilege
	// with no dedicated Capabilities flag of its own — capture's
	// Sys.Console (Capabilities.Capture's doc comment) can only be
	// confirmed absent when Sys.Modify (netWrite) is present and capture
	// still isn't; when Sys.Modify is ALSO absent, Explain still names
	// Sys.Console as required (RequiredPrivileges documents the pairing
	// publicly already) without claiming to know whether it too is
	// missing, since resolving that with certainty would need a fresh PVE
	// permissions read AC2/AC3 forbid.
	Confirmed bool `json:"confirmed"`
}

// Explanation is one capability's "why can't I?" answer, carried in a 403
// response's `details.explanation` (docs/api.md's error envelope) by
// RequireCap so the UI can render it without a second round trip
// re-deriving what RequireCap already knows.
//
// Field order here is fieldalignment/govet's (`Granted` last, to keep the
// bool out from between the pointer-bearing string/slice fields) rather
// than declaration order matching the doc-comment narrative below — see
// each field's own comment for what it means, not where it sits.
type Explanation struct {
	// Capability is the JSON field name of the capability that was
	// checked (a Cap's own string value, e.g. "netWrite").
	Capability string `json:"capability"`
	// Reason explains a denial Missing cannot: a capability with no PVE
	// privilege behind it at all (automation/automationWrite — token
	// scope only), or a session shape where naming one would either leak
	// (OIDC — see this file's doc comment) or mislead (bearer token, or a
	// `read_only` daemon). Empty when Granted is true or Missing is
	// populated.
	Reason string `json:"reason,omitempty"`
	// Missing lists every PVE privilege still absent for Capability, with
	// where it would need to be granted — more than one entry when
	// Capability requires holding several privileges at once (capture:
	// Sys.Modify AND Sys.Console). Empty when Granted is true, or when
	// Reason is set instead.
	Missing []PrivilegeRequirement `json:"missing,omitempty"`
	// Granted is whether the identity actually holds Capability. Always
	// false coming out of RequireCap's own denial path (see
	// middleware.go); exported so Explain's own unit tests, and any
	// future caller, can also ask about a capability the identity DOES
	// hold and get an unambiguous "yes" back rather than an empty struct.
	Granted bool `json:"granted"`
}

const (
	reasonNotPrivilegeDerived = "not derived from any PVE privilege — automation/automationWrite exist only as API token scopes minted via POST /tokens, never granted through a PVE ACL"
	reasonBearerToken         = "this request is authenticated by an API token; its capabilities come from the token's own minted scopes (POST /tokens), not a live PVE ACL privilege — grant it by minting a differently-scoped token, not by changing a PVE ACL"
	reasonOIDCIntersection    = "this session's capabilities are the intersection of an OIDC group policy and the linked PVE account's own ACLs (T-1207); which of the two is limiting this capability is deliberately not disclosed, so a caller cannot use this endpoint to learn a PVE grant their group policy is withholding"
	reasonForcedReadOnly      = "this daemon is running with [server] read_only = true, which forces this capability false regardless of the caller's PVE ACLs (handlers.go's forceReadOnly) — the fix is disabling read_only, not a PVE ACL grant the caller may already hold"
)

// forceReadOnlyClears is the exact capability set handlers.go's
// forceReadOnly zeroes — kept as its own small table (rather than
// importing behavior from that function) since Explain only needs the
// name, not the zeroing, and the two are pinned together by
// TestExplain_ForcedReadOnlyCapsMatchForceReadOnly.
var forceReadOnlyClears = map[Cap]bool{
	CapNetWrite:        true,
	CapSDNWrite:        true,
	CapFWWrite:         true,
	CapGuestNet:        true,
	CapCapture:         true,
	CapAutomationWrite: true,
}

// Explain answers "why can't I?" for cap, scoped to node exactly the way
// RequireCap's own id.HasCap(node, cap) check was scoped: node's own
// capability entry when the identity has one, otherwise the "any node"
// union every node in id.Caps grants (id.HasCap's documented fallback).
// Never performs a PVE round trip or re-derivation — it only reads the
// Capabilities id.Caps already holds, computed at login/renewal time by
// BuildCapabilities. readOnly is the daemon's own `[server] read_only`
// config (Service.readOnly) — RequireCap already has it in scope, and
// Explain needs it to recognize handlers.go's forceReadOnly rather than
// misreport a server-forced flag as a PVE ACL gap (see this file's doc
// comment).
func (id Identity) Explain(cap Cap, node string, readOnly bool) Explanation {
	entry, path := id.explainScope(node)
	exp := Explanation{Capability: string(cap), Granted: entry.Has(cap)}
	if exp.Granted {
		return exp
	}

	// Bearer-token, OIDC, and forced-read-only sessions all break the
	// "PVE privilege" story this function otherwise tells — see the file
	// doc comment for why each gets a Reason instead of a Missing list.
	// Checked before the per-capability table below so none ever falls
	// through to it.
	switch {
	case id.TokenID != "":
		exp.Reason = reasonBearerToken
		return exp
	case id.Realm == "oidc":
		exp.Reason = reasonOIDCIntersection
		return exp
	case readOnly && forceReadOnlyClears[cap]:
		exp.Reason = reasonForcedReadOnly
		return exp
	}

	missing := privilegeRequirementsFor(cap, entry, path)
	if missing == nil {
		exp.Reason = reasonNotPrivilegeDerived
		return exp
	}
	exp.Missing = missing
	return exp
}

// explainScope resolves the Capabilities value and the PVE ACL path label
// id.HasCap(node, cap) would have consulted for the same (node, cap) pair:
// node's own entry (path "/nodes/{node}") when id.Caps has one, otherwise
// the OR-across-every-node union (path "/", the one grant location
// guaranteed to cover every node id.Caps knows about) — id.HasCap's own
// "no entry for this node -> fall through to any node" fallback, expressed
// as data instead of a short-circuiting bool so Explain can still say
// exactly which flags are set afterward.
func (id Identity) explainScope(node string) (Capabilities, string) {
	if node != "" {
		if c, ok := id.Caps[node]; ok {
			return c, "/nodes/" + node
		}
	}
	var union Capabilities
	for _, c := range id.Caps {
		union = orCaps(union, c)
	}
	return union, "/"
}

// orCaps ORs two capability bundles flag-by-flag, every field including
// AutomationWrite. Deliberately not oidc_caps.go's unionCaps: that helper
// predates T-3003-followup-01's AutomationWrite split and does not carry
// it, which is safe for its own caller (OIDC group bundles never grant
// AutomationWrite — see MapGroupsToBundle) but would silently drop it here
// if id.Caps ever held two entries differing only in that flag.
func orCaps(a, b Capabilities) Capabilities {
	return Capabilities{
		NetRead:         a.NetRead || b.NetRead,
		NetWrite:        a.NetWrite || b.NetWrite,
		SDNRead:         a.SDNRead || b.SDNRead,
		SDNWrite:        a.SDNWrite || b.SDNWrite,
		FWRead:          a.FWRead || b.FWRead,
		FWWrite:         a.FWWrite || b.FWWrite,
		GuestNet:        a.GuestNet || b.GuestNet,
		Audit:           a.Audit || b.Audit,
		Automation:      a.Automation || b.Automation,
		AutomationWrite: a.AutomationWrite || b.AutomationWrite,
		Capture:         a.Capture || b.Capture,
	}
}

// privilegeRequirementsFor names the PVE privilege(s) backing cap (the
// same constants DeriveCapabilities's mapping table consults, per this
// package's "single source of truth" rule — see caps.go), and which of
// them entry's already-derived flags can confirm absent. Returns nil for
// a capability DeriveCapabilities never sets from any privilege
// (automation, automationWrite) — the caller reports Reason instead.
func privilegeRequirementsFor(cap Cap, entry Capabilities, path string) []PrivilegeRequirement {
	single := func(priv string) []PrivilegeRequirement {
		return []PrivilegeRequirement{{Privilege: priv, Path: path, Confirmed: true}}
	}
	switch cap {
	case CapNetRead, CapFWRead, CapAudit:
		// All three are exactly privSysAudit (DeriveCapabilities) —
		// entry.Has(cap) already being false confirms it absent.
		return single(privSysAudit)
	case CapNetWrite, CapFWWrite:
		return single(privSysModify)
	case CapSDNRead:
		return single(privSDNAudit)
	case CapSDNWrite:
		return single(privSDNAllocate)
	case CapGuestNet:
		return single(privVMConfigNet)
	case CapCapture:
		if !entry.NetWrite {
			// Sys.Modify (== netWrite) confirmed absent. Sys.Console has
			// no dedicated flag (Capabilities.Capture's doc comment), so
			// its state can't be confirmed without Sys.Modify already
			// resolved — still named as required (RequiredPrivileges
			// documents the pairing), not claimed confirmed-absent.
			return []PrivilegeRequirement{
				{Privilege: privSysModify, Path: path, Confirmed: true},
				{Privilege: privSysConsole, Path: path, Confirmed: false},
			}
		}
		// Sys.Modify held and capture still false: Sys.Console must be
		// the gap — deducible with certainty from entry.NetWrite alone.
		return single(privSysConsole)
	default:
		// CapAutomation, CapAutomationWrite: never PVE-privilege-derived.
		return nil
	}
}
