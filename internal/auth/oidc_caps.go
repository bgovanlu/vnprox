// SPDX-License-Identifier: Apache-2.0

package auth

// oidc_caps.go holds T-1207's group→role mapping and the authn/authz split
// enforcement. It extends internal/auth/caps.go's existing mapping-table
// pattern: caps.go maps a *PVE privilege set* to Capabilities
// (DeriveCapabilities); this file maps an *OIDC group claim set* to the same
// Capabilities vocabulary (MapGroupsToBundle), then caps that OIDC-derived
// bundle at the linked PVE identity's actual PVE ACLs (IntersectCaps) — so an
// OIDC-mapped capability is never additive beyond what the user's own PVE ACLs
// allow, mirroring docs/security.md's "vnprox cannot exceed the user's PVE
// ACLs" invariant.

// GroupMapping maps one OIDC group-claim value to a capability bundle. Order
// does not matter: MapGroupsToBundle takes the union across every matching
// group (a user in two mapped groups gets both bundles OR-ed), the natural
// "most-permissive-of-my-roles" semantics, still capped later by PVE ACLs.
type GroupMapping struct {
	// Group is the exact OIDC group-claim value (e.g. "vnprox-admins", or an
	// IdP-native group DN — matched verbatim, never interpreted).
	Group string
	// Caps is the capability bundle membership in Group grants, before the PVE
	// ACL cap is applied.
	Caps Capabilities
}

// MapGroupsToBundle returns the union of the capability bundles for every group
// in groups that has a mapping. Groups with no mapping contribute nothing; a
// user with no mapped group at all gets the zero Capabilities bundle (no
// vnprox-enforced capability), exactly the fail-closed default the PVE ticket
// bridge uses when capability derivation yields nothing.
func MapGroupsToBundle(groups []string, mappings []GroupMapping) Capabilities {
	byGroup := make(map[string]Capabilities, len(mappings))
	for _, m := range mappings {
		byGroup[m.Group] = unionCaps(byGroup[m.Group], m.Caps)
	}
	var bundle Capabilities
	for _, g := range groups {
		if c, ok := byGroup[g]; ok {
			bundle = unionCaps(bundle, c)
		}
	}
	return bundle
}

// unionCaps ORs two capability bundles flag-by-flag.
func unionCaps(a, b Capabilities) Capabilities {
	return Capabilities{
		NetRead:    a.NetRead || b.NetRead,
		NetWrite:   a.NetWrite || b.NetWrite,
		SDNRead:    a.SDNRead || b.SDNRead,
		SDNWrite:   a.SDNWrite || b.SDNWrite,
		FWRead:     a.FWRead || b.FWRead,
		FWWrite:    a.FWWrite || b.FWWrite,
		GuestNet:   a.GuestNet || b.GuestNet,
		Audit:      a.Audit || b.Audit,
		Automation: a.Automation || b.Automation,
		Capture:    a.Capture || b.Capture,
	}
}

// IntersectCaps ANDs two capability bundles flag-by-flag: the result grants a
// capability only if BOTH inputs do. This is the enforcement point of the
// authn/authz split — an OIDC-mapped bundle intersected with the linked PVE
// identity's PVE-derived caps can never exceed the PVE side (T-1207 AC3), and a
// user with no PVE linkage (an all-false PVE bundle) is left with no
// cluster-scoped capability at all despite any OIDC bundle (T-1207 AC2).
func IntersectCaps(a, b Capabilities) Capabilities {
	return Capabilities{
		NetRead:    a.NetRead && b.NetRead,
		NetWrite:   a.NetWrite && b.NetWrite,
		SDNRead:    a.SDNRead && b.SDNRead,
		SDNWrite:   a.SDNWrite && b.SDNWrite,
		FWRead:     a.FWRead && b.FWRead,
		FWWrite:    a.FWWrite && b.FWWrite,
		GuestNet:   a.GuestNet && b.GuestNet,
		Audit:      a.Audit && b.Audit,
		Automation: a.Automation && b.Automation,
		Capture:    a.Capture && b.Capture,
	}
}

// capLimiter is implemented by a PVEIdentity whose derived capabilities must be
// intersected with a fixed upper-bound bundle after DeriveCapabilities/
// BuildCapabilities runs. oidcIdentity implements it so that both the initial
// login derivation AND the hourly re-derivation (renewal.go) apply the OIDC
// group bundle as a ceiling automatically — the OIDC bundle can only ever
// narrow the PVE-derived caps, never widen them.
type capLimiter interface {
	// capBundle is the OIDC group→role bundle capping this identity's
	// PVE-derived capabilities.
	capBundle() Capabilities
}
