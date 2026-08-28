// SPDX-License-Identifier: Apache-2.0

package change

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// DefaultProtectedPath is where the onboarding-confirmed protected-
// interface set is persisted (docs/features/blueprints.md §3: "confirmation
// of detected management interfaces + corosync links... stored in
// /etc/pve/vnprox/protected.json"). Like corosync.conf, this path lives
// under /etc/pve — pmxcfs, the cluster's distributed filesystem — so every
// node's daemon reads/writes the same file, and the onboarding-confirmed
// set is inherently cluster-wide the moment it's saved on any one node.
const DefaultProtectedPath = "/etc/pve/vnprox/protected.json"

// protectedConfigVersion is ProtectedConfig's on-disk schema version
// (docs/features/blueprints.md's "versioned format" convention, mirrored
// here from blueprintVersion — a single integer bumped only on a breaking
// shape change).
const protectedConfigVersion = 1

// ProtectedConfig is protected.json's on-disk shape: a set of protected
// interface refs per node, confirmed (or corrected) by the admin during
// onboarding (docs/features/blueprints.md §3).
type ProtectedConfig struct {
	// Nodes maps a node name to the list of protected interface refs on
	// that node, each encoded as inventory.Ref.String() ("kind:node:id" —
	// the same encoding docs/api.md uses for every Ref in the API surface).
	Nodes map[string][]string `json:"nodes"`

	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	Version   int    `json:"version"`
}

// ProtectedSet is ProtectedConfig.Nodes resolved into inventory.Ref values
// — the shape safetyValidate actually consumes.
type ProtectedSet map[string][]inventory.Ref

// Resolve parses every ref string in c.Nodes into an inventory.Ref,
// returning the resolved set plus any ref strings that failed to parse. A
// hand-edited (or stale) protected.json should degrade gracefully rather
// than fail validation outright — bad refs are reported so the caller can
// log them, not silently dropped.
func (c ProtectedConfig) Resolve() (ProtectedSet, []string) {
	out := make(ProtectedSet, len(c.Nodes))
	var bad []string
	for node, refs := range c.Nodes {
		for _, s := range refs {
			ref, err := inventory.ParseRef(s)
			if err != nil {
				bad = append(bad, s)
				continue
			}
			out[node] = append(out[node], ref)
		}
	}
	return out, bad
}

// LoadProtectedConfig reads and parses path. A missing file is not an
// error: onboarding may not have run yet, and this returns an empty,
// zero-value config (Nodes non-nil but empty) so callers can treat "not
// configured yet" and "configured with nothing protected" identically.
func LoadProtectedConfig(path string) (ProtectedConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProtectedConfig{Nodes: map[string][]string{}}, nil
		}
		return ProtectedConfig{}, fmt.Errorf("change: reading protected-interface config %s: %w", path, err)
	}
	var cfg ProtectedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProtectedConfig{}, fmt.Errorf("change: parsing protected-interface config %s: %w", path, err)
	}
	if cfg.Nodes == nil {
		cfg.Nodes = map[string][]string{}
	}
	return cfg, nil
}

// SaveProtectedConfig atomically writes cfg to path: create the parent
// directory if needed, write to a temp file in the same directory, then
// rename over the target, so a concurrent reader (safetyValidate running
// against another request) never observes a partially-written file.
func SaveProtectedConfig(path string, cfg ProtectedConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("change: creating protected-interface config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("change: marshaling protected-interface config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".protected-*.json.tmp")
	if err != nil {
		return fmt.Errorf("change: creating temp protected-interface config file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("change: writing protected-interface config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("change: closing temp protected-interface config file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return fmt.Errorf("change: setting protected-interface config permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("change: renaming protected-interface config into place: %w", err)
	}
	return nil
}

// DetectProtected computes the onboarding-suggested protected-interface set
// from live inventory plus parsed corosync config (docs/features/
// blueprints.md §3: "detected management interfaces + corosync links...
// user confirms or corrects"). For each cluster node: every bridge or VLAN
// sub-interface (the only inventory kinds that declare an Addresses field
// — entity.go) whose address matches either that node's management IP
// (inventory.Node.IP, sourced from PVE cluster status) or one of its
// corosync ring addresses. cor may be nil (a single, not-yet-clustered
// node has no /etc/pve/corosync.conf at all) — DetectProtected then falls
// back to management-IP-only detection.
//
// This is a thin wrapper around DetectProtectedRoles (T-702's role-aware
// generalization, factored out so internal/topology's shared path resolver
// can consume the same classification this function was already doing):
// the flat ref set is exactly the union of every role-classified ref, which
// reproduces this function's pre-T-702 output byte-for-byte (same address
// matching, same per-node sort) — T-203's validator semantics (the only
// consumer that reads this specific function) are unaffected by the
// refactor.
func DetectProtected(snap inventory.Snapshot, cor *host.CorosyncConfig) ProtectedSet {
	roles := DetectProtectedRoles(snap, cor)
	out := ProtectedSet{}
	for node, refs := range roles {
		for _, rr := range refs {
			out[node] = append(out[node], rr.Ref)
		}
	}
	for node := range out {
		refs := out[node]
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		out[node] = refs
	}
	return out
}

// DetectProtectedRoles is DetectProtected's role-aware generalization
// (T-702, docs/features/topology.md §3 / docs/api.md's `GET
// /protected-interfaces/status`): same detection, but tags each protected
// ref with which purpose(s) it serves — RoleMgmt when it carries the node's
// management IP, RoleCorosync when it carries one of the node's corosync
// ring addresses (both possible on one ref). The result is directly the
// input shape internal/topology.ResolveMgmtPaths consumes.
func DetectProtectedRoles(snap inventory.Snapshot, cor *host.CorosyncConfig) map[string][]topology.MgmtRoleRef {
	out := map[string][]topology.MgmtRoleRef{}

	for _, e := range snap.All() {
		node, ok := e.(*inventory.Node)
		if !ok {
			continue
		}
		wanted := wantedRoleAddrs(node, cor)
		if len(wanted) == 0 {
			continue
		}

		for _, e2 := range snap.All() {
			ref := e2.GetRef()
			if ref.Node != node.Name {
				continue
			}
			addrs, ok := addressesOf(e2)
			if !ok {
				continue
			}
			roles := matchRoles(addrs, wanted)
			if len(roles) == 0 {
				continue
			}
			out[node.Name] = append(out[node.Name], topology.MgmtRoleRef{Ref: ref, Roles: roles})
		}
	}

	for node := range out {
		refs := out[node]
		sort.Slice(refs, func(i, j int) bool { return refs[i].Ref.String() < refs[j].Ref.String() })
		out[node] = refs
	}
	return out
}

// classifyConfirmedRoles is DetectProtectedRoles' counterpart for an
// onboarding-confirmed protected set (protected.json non-empty): the admin
// already picked the refs, so this only computes which role(s) each one
// currently serves (for display — docs/api.md's `GET
// /protected-interfaces/status` roles field), via the exact same address
// matching DetectProtectedRoles uses. A confirmed ref that currently
// carries neither the node's management IP nor a corosync ring address
// (stale confirmation, or a physnic/bond ref — see
// protectedIPsForNode's doc comment on that known scope gap) simply gets an
// empty Roles slice rather than being dropped: the admin explicitly
// protected it, so the status endpoint still reports its (unresolved) path.
func classifyConfirmedRoles(snap inventory.Snapshot, cor *host.CorosyncConfig, refs ProtectedSet) map[string][]topology.MgmtRoleRef {
	nodesByName := map[string]*inventory.Node{}
	for _, e := range snap.All() {
		if n, ok := e.(*inventory.Node); ok {
			nodesByName[n.Name] = n
		}
	}

	out := make(map[string][]topology.MgmtRoleRef, len(refs))
	for nodeName, nodeRefs := range refs {
		var wanted map[string][]topology.MgmtRole
		if n := nodesByName[nodeName]; n != nil {
			wanted = wantedRoleAddrs(n, cor)
		}
		list := make([]topology.MgmtRoleRef, 0, len(nodeRefs))
		for _, r := range nodeRefs {
			var roles []topology.MgmtRole
			if e, ok := snap.Get(r); ok {
				if addrs, ok := addressesOf(e); ok {
					roles = matchRoles(addrs, wanted)
				}
			}
			list = append(list, topology.MgmtRoleRef{Ref: r, Roles: roles})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Ref.String() < list[j].Ref.String() })
		out[nodeName] = list
	}
	return out
}

// addressesOf returns e's declared CIDR addresses and whether e is a kind
// that declares an Addresses field at all (Bridge/VlanIface — see
// entity.go).
func addressesOf(e inventory.Entity) ([]string, bool) {
	switch v := e.(type) {
	case *inventory.Bridge:
		return v.Addresses, true
	case *inventory.VlanIface:
		return v.Addresses, true
	default:
		return nil, false
	}
}

// wantedRoleAddrs builds node's role-tagged wanted-address set: its
// management IP (RoleMgmt) plus every corosync ring address cor reports for
// it (RoleCorosync) — the same two sources DetectProtected has always
// matched against, just no longer flattened into a single boolean set.
func wantedRoleAddrs(node *inventory.Node, cor *host.CorosyncConfig) map[string][]topology.MgmtRole {
	if node == nil {
		return nil
	}
	wanted := map[string][]topology.MgmtRole{}
	addRole := func(addr string, role topology.MgmtRole) {
		if addr == "" {
			return
		}
		for _, r := range wanted[addr] {
			if r == role {
				return
			}
		}
		wanted[addr] = append(wanted[addr], role)
	}
	if node.IP != "" {
		addRole(node.IP, topology.MgmtRoleMgmt)
	}
	if cn, ok := cor.NodeByName(node.Name); ok {
		for _, addr := range cn.RingAddrs {
			addRole(addr, topology.MgmtRoleCorosync)
		}
	}
	return wanted
}

// matchRoles reports which role(s) addrs (declared CIDR strings) satisfy
// against wanted (addr -> roles, from wantedRoleAddrs) — the role-aware
// generalization of the old addrMatchesAny boolean check, same dual
// matching (raw string first, then parsed host IP, since PVE's `GET
// /cluster/status` and corosync.conf's ring*_addr both commonly report a
// bare IP while inventory.Bridge/VlanIface.Addresses are always CIDRs).
// `len(matchRoles(addrs, wanted)) > 0` is exactly the predicate
// addrMatchesAny used to compute, so DetectProtected's ref-inclusion set is
// unchanged by this refactor.
func matchRoles(addrs []string, wanted map[string][]topology.MgmtRole) []topology.MgmtRole {
	if len(wanted) == 0 {
		return nil
	}
	var roles []topology.MgmtRole
	seen := map[topology.MgmtRole]bool{}
	collect := func(key string) {
		for _, r := range wanted[key] {
			if !seen[r] {
				seen[r] = true
				roles = append(roles, r)
			}
		}
	}
	for _, a := range addrs {
		collect(a)
		if ip, _, err := net.ParseCIDR(a); err == nil {
			collect(ip.String())
		}
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	return roles
}

// ToConfig snapshots set into a ProtectedConfig.Nodes-shaped map
// (inventory.Ref.String() encoding), for building a ProtectedConfig from a
// freshly detected ProtectedSet (e.g. the onboarding API's "suggest"
// response) without hand-rolling the string conversion at each call site.
func (set ProtectedSet) ToConfig() map[string][]string {
	out := make(map[string][]string, len(set))
	for node, refs := range set {
		ids := make([]string, len(refs))
		for i, r := range refs {
			ids[i] = r.String()
		}
		out[node] = ids
	}
	return out
}

// MgmtStatus is docs/api.md's `GET /protected-interfaces/status` response
// shape (T-702): per-node resolved management paths, plus which source fed
// them. Source is "confirmed" when protected.json has at least one node
// entry (the admin's onboarding-confirmed set, classified against current
// roles), or "detected" when it's empty and this falls back to live
// DetectProtectedRoles (docs/features/blueprints.md §3's "confirmation ...
// stored in protected.json" — an unconfirmed cluster still gets a display
// answer, just an explicitly provisional one). BadRefs carries any
// protected.json ref strings that failed to parse (ProtectedConfig.Resolve
// — surfaced so the caller can warn, mirroring SetProtected's existing
// bad-ref handling).
// StaleProtected (T-703) is true iff Source is "confirmed" but live
// detection currently finds a management/corosync carrier the confirmed set
// does not contain — the tell-tale of a management path that has *moved*
// since onboarding confirmed it (e.g. the dedicated-management-VLAN flow
// relocating the mgmt address to a new carrier, then the operator declining
// the post-commit protected-set refresh). Deliberately one-directional:
// confirmed refs that detection can't classify (an admin protecting a bond
// or an extra interface on purpose) never flag staleness.
type MgmtStatus struct {
	Source         string
	Nodes          map[string][]topology.MgmtPath
	BadRefs        []string
	StaleProtected bool
}
