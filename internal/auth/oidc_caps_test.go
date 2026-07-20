package auth_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// fullWrite is a capability bundle granting every write flag (what an OIDC
// "admins" group might map to before the PVE cap is applied).
var fullWrite = auth.Capabilities{
	NetRead: true, NetWrite: true, SDNRead: true, SDNWrite: true,
	FWRead: true, FWWrite: true, GuestNet: true, Audit: true,
}

// readOnly models an auditor's PVE-derived caps (Sys.Audit/SDN.Audit only).
var readOnly = auth.Capabilities{NetRead: true, SDNRead: true, FWRead: true, Audit: true}

func TestMapGroupsToBundle(t *testing.T) {
	mappings := []auth.GroupMapping{
		{Group: "vnprox-admins", Caps: fullWrite},
		{Group: "vnprox-readers", Caps: readOnly},
		{Group: "net-team", Caps: auth.Capabilities{NetRead: true, NetWrite: true}},
	}
	tests := []struct {
		name   string
		groups []string
		want   auth.Capabilities
	}{
		{"single mapped group", []string{"vnprox-admins"}, fullWrite},
		{"unmapped group only", []string{"random-corp-group"}, auth.Capabilities{}},
		{"no groups", nil, auth.Capabilities{}},
		{
			"union of two mapped groups is most-permissive",
			[]string{"vnprox-readers", "net-team"},
			auth.Capabilities{NetRead: true, NetWrite: true, SDNRead: true, FWRead: true, Audit: true},
		},
		{"mapped + unmapped mix keeps the mapped bundle", []string{"net-team", "nope"},
			auth.Capabilities{NetRead: true, NetWrite: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.MapGroupsToBundle(tc.groups, mappings); got != tc.want {
				t.Errorf("MapGroupsToBundle(%v) = %+v, want %+v", tc.groups, got, tc.want)
			}
		})
	}
}

// TestIntersectCaps_AuthzSplit is the unit-level proof of T-1207's authn/authz
// split: an OIDC-mapped bundle intersected with the linked PVE identity's caps
// can never exceed the PVE side (AC3), and a user with no PVE linkage (all-false
// PVE caps) is left with no capability at all despite any OIDC bundle (AC2).
func TestIntersectCaps_AuthzSplit(t *testing.T) {
	tests := []struct {
		name string
		oidc auth.Capabilities
		pve  auth.Capabilities
		want auth.Capabilities
	}{
		{
			name: "AC3: OIDC bundle capped at read-only PVE ACLs",
			oidc: fullWrite,
			pve:  readOnly,
			want: readOnly, // every write flag stripped by the PVE cap
		},
		{
			name: "AC2: no PVE linkage denies every capability despite full OIDC bundle",
			oidc: fullWrite,
			pve:  auth.Capabilities{}, // no linkage → all-false PVE caps
			want: auth.Capabilities{},
		},
		{
			name: "PVE grants more than OIDC: still capped at the OIDC bundle",
			oidc: auth.Capabilities{NetRead: true},
			pve:  fullWrite,
			want: auth.Capabilities{NetRead: true},
		},
		{
			name: "agreeing flags survive",
			oidc: auth.Capabilities{NetRead: true, NetWrite: true},
			pve:  auth.Capabilities{NetRead: true, NetWrite: true, SDNRead: true},
			want: auth.Capabilities{NetRead: true, NetWrite: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.IntersectCaps(tc.oidc, tc.pve); got != tc.want {
				t.Errorf("IntersectCaps(%+v, %+v) = %+v, want %+v", tc.oidc, tc.pve, got, tc.want)
			}
			// Intersection is symmetric.
			if got := auth.IntersectCaps(tc.pve, tc.oidc); got != tc.want {
				t.Errorf("IntersectCaps not symmetric: got %+v, want %+v", got, tc.want)
			}
		})
	}
}
