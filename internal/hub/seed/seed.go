// Package seed is T-2104's seeded blueprint library: the "real-world"
// blueprints the hosted registry (docs/hub-registry.md, T-2803) ships once
// hosting exists — homelab single-node, three-node Ceph storage, a
// VLAN-segmented SMB branch office, and a DMZ fronting a WireGuard
// site-to-site tunnel.
//
// These are content, not runtime code: each is a plain *blueprint.Blueprint,
// built the same way internal/blueprint's own five bundled starters are
// (starters.go — literals, not embedded JSON, so a typo fails the build's
// tests rather than a user's instantiate call; see seed_test.go). Unlike a
// starter, a seed is not wired into the daemon's own /blueprints listing —
// it exists to be signed and published through the T-2803 submission/review
// process (`vnproxctl hub publish` / `hub index`, docs/hub-registry.md) as
// the registry's first real content. cmd/vnproxctl's
// TestHubCLI_SeedBlueprintsPublishReviewIndex walks that process once, end
// to end, with each of these as the "real bundle" (T-2104 AC4); until a
// registry is actually hosted (docs/hub-registry.md's "Status" section),
// nothing here is published anywhere.
//
// WireGuard caveat, narrowed by T-3303 (mirrors starters.go's EVPN-starter
// caveat, same reasoning, smaller scope now): blueprint.KindWgTunnel closed
// the "no wg.* entity kind" gap this package used to describe in full —
// SeedDMZWireGuardSiteToSite now provisions the DMZ segment AND the local
// WireGuard tunnel interface. What is still out of reach is the remote
// PEER: a wg.peer.add op needs the remote site's own public key, exchanged
// out of band, which cannot exist at instantiation time — see
// seedDMZWireGuardSiteToSite's own doc comment.
package seed

import "github.com/bgovanlu/vnprox/internal/blueprint"

const (
	SeedHomelabSingleNode      = "seed-homelab-single-node"
	SeedCeph3NodeStorage       = "seed-ceph-3node-storage"
	SeedSMBVLANSegmented       = "seed-smb-vlan-segmented"
	SeedDMZWireGuardSiteToSite = "seed-dmz-wireguard-site-to-site"
)

// Seeds returns the four bundled real-world blueprints, freshly built each
// call so a caller mutating one returned value never corrupts another
// caller's copy (mirrors blueprint.Starters' own convention).
func Seeds() []*blueprint.Blueprint {
	return []*blueprint.Blueprint{
		seedHomelabSingleNode(),
		seedCeph3NodeStorage(),
		seedSMBVLANSegmented(),
		seedDMZWireGuardSiteToSite(),
	}
}

// ByID returns one seed blueprint by id, or ok=false.
func ByID(id string) (*blueprint.Blueprint, bool) {
	for _, s := range Seeds() {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

func seedHomelabSingleNode() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               SeedHomelabSingleNode,
		Name:             "Homelab (single node)",
		Description: "One uplink NIC forming a VLAN-aware LAN bridge with a static gateway and a set " +
			"of guest VLANs trunked through it — the common single-box homelab: one Proxmox host, " +
			"one router-facing uplink, everything else segmented by VLAN.",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params: []blueprint.ParamDef{
			{Name: "uplink", Type: blueprint.ParamIface, Label: "Uplink NIC", Default: "eno1", Required: true},
			{Name: "bridgeName", Type: blueprint.ParamString, Label: "LAN bridge name", Default: "vmbr0", Required: true},
			{Name: "lanCidr", Type: blueprint.ParamCIDR, Label: "LAN address", Default: "192.168.1.10/24",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "gateway", Type: blueprint.ParamIP, Label: "Upstream gateway", Default: "192.168.1.1",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "guestVlans", Type: blueprint.ParamVIDList, Label: "Guest VLANs", Default: []any{10, 20, 30}, Required: true},
		},
		Entities: []blueprint.EntityTemplate{
			{
				Kind:       blueprint.KindBridge,
				IDTemplate: "{{bridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{uplink}}"},
					"vlanAware": true,
					"vids":      "{{guestVlans}}",
					"addresses": []any{"{{lanCidr}}"},
					"gateway":   "{{gateway}}",
					"comments":  "vnprox seed: homelab-single-node",
				},
			},
		},
	}
}

// seedCeph3NodeStorage builds a cluster-wide VXLAN overlay dedicated to Ceph
// public/cluster traffic, spanning the target nodes — the "define once,
// apply to N nodes" flagship (docs/features/blueprints.md §2), scoped
// cluster-wide (SelectSingle) rather than per-node exactly like the bundled
// VXLAN-overlay starter: a shared overlay subnet, not a per-node static
// address, is what actually stays collision-free when applied across three
// nodes at once (a per-node bridge/bond address param, unlike this, would
// assign the identical literal address to every target node — see the
// bundled LACP-bond-storage starter, whose own tests only ever target one
// node for exactly that reason).
func seedCeph3NodeStorage() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               SeedCeph3NodeStorage,
		Name:             "Three-node Ceph cluster storage network",
		Description: "A cluster-wide VXLAN overlay dedicated to Ceph public/cluster traffic, one VNet " +
			"and one subnet, spanning a three-node Ceph cluster — apply once against all three nodes " +
			"to give every OSD/mon a shared, routable storage segment with no per-switch VLAN " +
			"provisioning.",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectSingle},
		Params: []blueprint.ParamDef{
			{Name: "zoneId", Type: blueprint.ParamString, Label: "Zone id", Default: "cephstorage", Required: true},
			{Name: "vnetName", Type: blueprint.ParamString, Label: "VNet name", Default: "cephnet", Required: true},
			{Name: "vni", Type: blueprint.ParamInt, Label: "VXLAN VNI (vrfVxlan)", Default: 10500, Required: true},
			{Name: "storageCidr", Type: blueprint.ParamCIDR, Label: "Storage subnet", Default: "10.50.0.0/24", Required: true},
			{Name: "storageGateway", Type: blueprint.ParamIP, Label: "Storage gateway", Default: "10.50.0.1",
				Required: true, AddressSuggest: true, Subnet: "10.50.0.0/24"},
		},
		Entities: []blueprint.EntityTemplate{
			{
				Kind:       blueprint.KindSdnZone,
				IDTemplate: "{{zoneId}}",
				Fields: map[string]any{
					"type":     "vxlan",
					"nodes":    "{{__nodes__}}",
					"vrfVxlan": "{{vni}}",
				},
			},
			{
				Kind:       blueprint.KindSdnVnet,
				IDTemplate: "{{zoneId}}/{{vnetName}}",
				Fields: map[string]any{
					"zone": "{{zoneId}}",
				},
			},
			{
				Kind:       blueprint.KindSdnSubnet,
				IDTemplate: "{{storageCidr}}",
				Fields: map[string]any{
					"vnet":    "{{zoneId}}/{{vnetName}}",
					"cidr":    "{{storageCidr}}",
					"gateway": "{{storageGateway}}",
				},
			},
		},
	}
}

func seedSMBVLANSegmented() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               SeedSMBVLANSegmented,
		Name:             "VLAN-segmented SMB branch office",
		Description: "A dedicated management bridge on one NIC and a VLAN-aware trunk bridge on a " +
			"second NIC carrying the branch office's department VLANs (staff, guest wifi, servers, " +
			"voice) — for a small/medium business site with one Proxmox host acting as the branch " +
			"router, where management traffic must stay off the department trunk.",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params: []blueprint.ParamDef{
			{Name: "mgmtNic", Type: blueprint.ParamIface, Label: "Management NIC", Default: "eno1", Required: true},
			{Name: "trunkNic", Type: blueprint.ParamIface, Label: "Department trunk NIC", Default: "eno2", Required: true},
			{Name: "mgmtBridgeName", Type: blueprint.ParamString, Label: "Management bridge name", Default: "vmbr0", Required: true},
			{Name: "trunkBridgeName", Type: blueprint.ParamString, Label: "Trunk bridge name", Default: "vmbr1", Required: true},
			{Name: "mgmtCidr", Type: blueprint.ParamCIDR, Label: "Management address", Default: "192.168.1.10/24",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "departmentVlans", Type: blueprint.ParamVIDList, Label: "Department VLANs (staff/guest-wifi/servers/voice)",
				Default: []any{10, 20, 30, 40}, Required: true},
		},
		Entities: []blueprint.EntityTemplate{
			{
				Kind:       blueprint.KindBridge,
				IDTemplate: "{{mgmtBridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{mgmtNic}}"},
					"addresses": []any{"{{mgmtCidr}}"},
					"comments":  "vnprox seed: smb-vlan-segmented (management)",
				},
			},
			{
				Kind:       blueprint.KindBridge,
				IDTemplate: "{{trunkBridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{trunkNic}}"},
					"vlanAware": true,
					"vids":      "{{departmentVlans}}",
					"comments":  "vnprox seed: smb-vlan-segmented (department VLAN trunk: staff/guest-wifi/servers/voice)",
				},
			},
		},
	}
}

// seedDMZWireGuardSiteToSite provisions the DMZ network segment AND the
// local WireGuard tunnel interface fronting it (T-3303: blueprint.KindWgTunnel
// closed the "no wg.* entity kind" gap this blueprint used to name). Still
// PARTIAL, narrower now: the REMOTE PEER is not, and cannot be, templated
// here — a wg.peer.add op needs the remote site's own public key, which is
// exchanged out of band and does not exist at instantiation time. Adding
// the peer is a `wg.peer.add` op against the tunnel this blueprint creates,
// via vnprox's own WireGuard support (docs/features/wireguard.md), once
// that key is in hand.
func seedDMZWireGuardSiteToSite() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		BlueprintVersion: blueprint.CurrentBlueprintVersion,
		ID:               SeedDMZWireGuardSiteToSite,
		Name:             "DMZ fronting a WireGuard site-to-site tunnel",
		Description: "An isolated DMZ bridge on its own NIC, addressed out of a dedicated subnet, plus " +
			"the local WireGuard tunnel interface that rides on it, for a host fronting a site-to-site " +
			"tunnel to a remote site. PARTIAL: this blueprint creates the DMZ segment and the tunnel " +
			"interface, but not the remote peer — a wg.peer.add op needs the remote site's public key, " +
			"exchanged out of band, which does not exist at instantiation time. Add the peer via " +
			"vnprox's own WireGuard support (docs/features/wireguard.md) once you have it.",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params: []blueprint.ParamDef{
			{Name: "dmzNic", Type: blueprint.ParamIface, Label: "DMZ NIC", Default: "eno2", Required: true},
			{Name: "dmzBridgeName", Type: blueprint.ParamString, Label: "DMZ bridge name", Default: "vmbr-dmz", Required: true},
			{Name: "dmzCidr", Type: blueprint.ParamCIDR, Label: "DMZ address", Default: "172.16.99.1/28",
				Required: true, AddressSuggest: true, Subnet: "172.16.99.0/28"},
			{Name: "wgIfName", Type: blueprint.ParamString, Label: "WireGuard interface name", Default: "wg0", Required: true},
			{Name: "wgListenPort", Type: blueprint.ParamInt, Label: "WireGuard listen port", Default: 51820, Required: true},
		},
		Entities: []blueprint.EntityTemplate{
			{
				Kind:       blueprint.KindBridge,
				IDTemplate: "{{dmzBridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{dmzNic}}"},
					"addresses": []any{"{{dmzCidr}}"},
					"comments": "vnprox seed: dmz-wireguard-site-to-site (DMZ segment; the WireGuard " +
						"tunnel interface rides on this bridge)",
				},
			},
			{
				Kind:       blueprint.KindWgTunnel,
				IDTemplate: "{{wgIfName}}",
				Fields: map[string]any{
					"ifName":     "{{wgIfName}}",
					"carrier":    "{{dmzBridgeName}}",
					"listenPort": "{{wgListenPort}}",
				},
			},
		},
	}
}
