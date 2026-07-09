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
func DetectProtected(snap inventory.Snapshot, cor *host.CorosyncConfig) ProtectedSet {
	out := ProtectedSet{}

	for _, e := range snap.All() {
		node, ok := e.(*inventory.Node)
		if !ok {
			continue
		}

		wanted := map[string]bool{}
		if node.IP != "" {
			wanted[node.IP] = true
		}
		if cn, ok := cor.NodeByName(node.Name); ok {
			for _, addr := range cn.RingAddrs {
				wanted[addr] = true
			}
		}
		if len(wanted) == 0 {
			continue
		}

		for _, e2 := range snap.All() {
			ref := e2.GetRef()
			if ref.Node != node.Name {
				continue
			}
			var addrs []string
			switch v := e2.(type) {
			case *inventory.Bridge:
				addrs = v.Addresses
			case *inventory.VlanIface:
				addrs = v.Addresses
			default:
				continue
			}
			if addrMatchesAny(addrs, wanted) {
				out[node.Name] = append(out[node.Name], ref)
			}
		}
	}

	for node := range out {
		refs := out[node]
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		out[node] = refs
	}
	return out
}

// addrMatchesAny reports whether any of addrs (declared CIDR strings)
// names an IP present in wanted (a set of raw IP or CIDR strings — PVE's
// GET /cluster/status and corosync.conf's ring*_addr both commonly report
// a bare IP, while inventory.Bridge/VlanIface.Addresses are always
// CIDRs, so this compares by parsed host IP first and falls back to a raw
// string match for anything that doesn't parse as a CIDR).
func addrMatchesAny(addrs []string, wanted map[string]bool) bool {
	for _, a := range addrs {
		if wanted[a] {
			return true
		}
		if ip, _, err := net.ParseCIDR(a); err == nil && wanted[ip.String()] {
			return true
		}
	}
	return false
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
