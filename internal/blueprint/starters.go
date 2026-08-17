// starters.go implements docs/features/blueprints.md §1's "ship with
// starters (bundled, read-only, copy-to-edit)": the five documented
// templates, each with a description and (via the wizard-preview
// machinery — see web/src/blueprints/preview.ts) a rendered diagram from
// its own entities. Built as Go literals (rather than embedded JSON files)
// since a literal *Blueprint already round-trips through exactly the same
// JSON (un)marshaling every other caller uses (Validate/Instantiate never
// distinguish a literal-built Blueprint from a json.Unmarshal'd one — both
// are just *Blueprint values with a map[string]any Fields) and needs no
// embed.FS wiring for five small, static templates; each one is asserted
// against Validate in starters_test.go so a typo here fails the build's
// tests, not a user's instantiate call.
//
// SDN starter note (T-603 report originally flagged this; closed by T-3102):
// the VXLAN overlay starter is fully self-sufficient (a vxlan zone needs no
// pre-existing PVE object). The EVPN starter used to be partial — PVE's EVPN
// zone type references a controller by ID (SdnZoneCreateParams.Controller),
// and this codebase's op vocabulary had no `sdn.controller.create` op to
// create one with, so the starter assumed a controller already existed and
// only created the zone/vnet/subnet that reference it. T-3102 added
// `sdn.controller.*` (internal/change/op.go), so starterEVPNDatacenter now
// creates its own controller as the first entity in its list (a bgp-type
// controller — see that function's own doc comment for why bgp rather than
// evpn) and completes end-to-end.

package blueprint

const (
	StarterSingleNICHomelab = "starter-single-nic-homelab"
	StarterDualNICMgmtTrunk = "starter-dual-nic-mgmt-trunk"
	StarterLACPBondStorage  = "starter-lacp-bond-storage-vlan"
	StarterVXLANOverlay     = "starter-vxlan-overlay"
	StarterEVPNDatacenter   = "starter-evpn-datacenter"
)

// Starters returns the five bundled read-only blueprints, freshly built
// each call so a caller mutating one returned value (e.g. Service.List
// stamping in list-response-only fields) never corrupts another caller's
// copy.
func Starters() []*Blueprint {
	return []*Blueprint{
		starterSingleNICHomelab(),
		starterDualNICMgmtTrunk(),
		starterLACPBondStorage(),
		starterVXLANOverlay(),
		starterEVPNDatacenter(),
	}
}

// StarterByID returns one starter by id, or ok=false.
func StarterByID(id string) (*Blueprint, bool) {
	for _, s := range Starters() {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

func starterSingleNICHomelab() *Blueprint {
	return &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               StarterSingleNICHomelab,
		Name:             "Single NIC homelab",
		Description: "One uplink NIC carrying a single VLAN-aware management bridge with a set of " +
			"guest VLANs trunked through it. For a single-NIC host (or a lab VM) where everything — " +
			"management and every guest VLAN — shares the one link.",
		ReadOnly:     true,
		NodeSelector: NodeSelector{Mode: SelectAll},
		Params: []ParamDef{
			{Name: "uplink", Type: ParamIface, Label: "Uplink NIC", Default: "eno1", Required: true},
			{Name: "bridgeName", Type: ParamString, Label: "Bridge name", Default: "vmbr0", Required: true},
			{Name: "mgmtCidr", Type: ParamCIDR, Label: "Management address", Default: "192.168.1.10/24",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "guestVlans", Type: ParamVIDList, Label: "Guest VLANs", Default: []any{10, 20, 30}, Required: true},
		},
		Entities: []EntityTemplate{
			{
				Kind:       KindBridge,
				IDTemplate: "{{bridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{uplink}}"},
					"vlanAware": true,
					"vids":      "{{guestVlans}}",
					"addresses": []any{"{{mgmtCidr}}"},
					"comments":  "vnprox blueprint: single-nic-homelab",
				},
			},
		},
	}
}

func starterDualNICMgmtTrunk() *Blueprint {
	return &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               StarterDualNICMgmtTrunk,
		Name:             "Dual NIC: mgmt + trunk",
		Description: "A dedicated management bridge on one NIC and a separate VLAN-aware trunk bridge " +
			"on the second NIC, for guest VLANs — keeps management traffic off the same wire as guest " +
			"VLAN trunk traffic.",
		ReadOnly:     true,
		NodeSelector: NodeSelector{Mode: SelectAll},
		Params: []ParamDef{
			{Name: "mgmtNic", Type: ParamIface, Label: "Management NIC", Default: "eno1", Required: true},
			{Name: "trunkNic", Type: ParamIface, Label: "Trunk NIC", Default: "eno2", Required: true},
			{Name: "mgmtBridgeName", Type: ParamString, Label: "Management bridge name", Default: "vmbr0", Required: true},
			{Name: "trunkBridgeName", Type: ParamString, Label: "Trunk bridge name", Default: "vmbr1", Required: true},
			{Name: "mgmtCidr", Type: ParamCIDR, Label: "Management address", Default: "192.168.1.10/24",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "guestVlans", Type: ParamVIDList, Label: "Guest VLANs", Default: []any{10, 20, 30}, Required: true},
		},
		Entities: []EntityTemplate{
			{
				Kind:       KindBridge,
				IDTemplate: "{{mgmtBridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{mgmtNic}}"},
					"addresses": []any{"{{mgmtCidr}}"},
					"comments":  "vnprox blueprint: dual-nic-mgmt-trunk (management)",
				},
			},
			{
				Kind:       KindBridge,
				IDTemplate: "{{trunkBridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{trunkNic}}"},
					"vlanAware": true,
					"vids":      "{{guestVlans}}",
					"comments":  "vnprox blueprint: dual-nic-mgmt-trunk (guest trunk)",
				},
			},
		},
	}
}

func starterLACPBondStorage() *Blueprint {
	return &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               StarterLACPBondStorage,
		Name:             "2-port LACP bond + storage VLAN",
		Description: "Two NICs bonded with 802.3ad LACP, carrying a management bridge plus a dedicated " +
			"VLAN sub-interface for storage traffic (e.g. iSCSI/NFS) — for hosts with a switch that " +
			"supports LACP and a separate storage VLAN.",
		ReadOnly:     true,
		NodeSelector: NodeSelector{Mode: SelectAll},
		Params: []ParamDef{
			{Name: "nic1", Type: ParamIface, Label: "First bonded NIC", Default: "eno1", Required: true},
			{Name: "nic2", Type: ParamIface, Label: "Second bonded NIC", Default: "eno2", Required: true},
			{Name: "bondName", Type: ParamString, Label: "Bond name", Default: "bond0", Required: true},
			{Name: "bridgeName", Type: ParamString, Label: "Management bridge name", Default: "vmbr0", Required: true},
			{Name: "mgmtCidr", Type: ParamCIDR, Label: "Management address", Default: "192.168.1.10/24",
				Required: true, AddressSuggest: true, Subnet: "192.168.1.0/24"},
			{Name: "storageVid", Type: ParamVID, Label: "Storage VLAN id", Default: 30, Required: true},
			{Name: "storageCidr", Type: ParamCIDR, Label: "Storage address", Default: "10.30.0.10/24",
				Required: true, AddressSuggest: true, Subnet: "10.30.0.0/24"},
		},
		Entities: []EntityTemplate{
			{
				Kind:       KindBond,
				IDTemplate: "{{bondName}}",
				Fields: map[string]any{
					"mode":   "802.3ad",
					"slaves": []any{"{{nic1}}", "{{nic2}}"},
				},
			},
			{
				Kind:       KindBridge,
				IDTemplate: "{{bridgeName}}",
				Fields: map[string]any{
					"ports":     []any{"{{bondName}}"},
					"addresses": []any{"{{mgmtCidr}}"},
					"comments":  "vnprox blueprint: lacp-bond-storage-vlan (management)",
				},
			},
			{
				Kind:       KindVlan,
				IDTemplate: "{{bondName}}.{{storageVid}}",
				Fields: map[string]any{
					"parent":    "{{bondName}}",
					"vid":       "{{storageVid}}",
					"addresses": []any{"{{storageCidr}}"},
				},
			},
		},
	}
}

func starterVXLANOverlay() *Blueprint {
	return &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               StarterVXLANOverlay,
		Name:             "3-node cluster with VXLAN overlay",
		Description: "A cluster-wide VXLAN zone spanning the target nodes, one VNet, and one overlay " +
			"subnet — the flagship 'define the node network once, apply to N nodes' use case " +
			"(docs/features/blueprints.md §2), for an overlay network reachable from every node without " +
			"per-switch VLAN provisioning.",
		ReadOnly:     true,
		NodeSelector: NodeSelector{Mode: SelectSingle},
		Params: []ParamDef{
			{Name: "zoneId", Type: ParamString, Label: "Zone id", Default: "vxzone1", Required: true},
			{Name: "vnetName", Type: ParamString, Label: "VNet name", Default: "vxnet1", Required: true},
			{Name: "vni", Type: ParamInt, Label: "VXLAN VNI (vrfVxlan)", Default: 10000, Required: true},
			{Name: "overlayCidr", Type: ParamCIDR, Label: "Overlay subnet", Default: "10.100.0.0/24", Required: true},
			{Name: "overlayGateway", Type: ParamIP, Label: "Overlay gateway", Default: "10.100.0.1",
				Required: true, AddressSuggest: true, Subnet: "10.100.0.0/24"},
		},
		Entities: []EntityTemplate{
			{
				Kind:       KindSdnZone,
				IDTemplate: "{{zoneId}}",
				Fields: map[string]any{
					"type":     "vxlan",
					"nodes":    "{{__nodes__}}",
					"vrfVxlan": "{{vni}}",
				},
			},
			{
				Kind:       KindSdnVnet,
				IDTemplate: "{{zoneId}}/{{vnetName}}",
				Fields: map[string]any{
					"zone": "{{zoneId}}",
				},
			},
			{
				Kind:       KindSdnSubnet,
				IDTemplate: "{{overlayCidr}}",
				Fields: map[string]any{
					"vnet":    "{{zoneId}}/{{vnetName}}",
					"cidr":    "{{overlayCidr}}",
					"gateway": "{{overlayGateway}}",
				},
			},
		},
	}
}

// starterEVPNDatacenter builds a controller + EVPN zone/VNet/subnet for a
// BGP-EVPN datacenter fabric (T-3102: completes the T-603 gap this file's
// own doc comment describes). The controller entity is listed FIRST — like
// every other multi-entity starter in this file, entities are expanded and
// diffed in list order (EntityTemplate's own doc comment), and the zone's
// "controller" field is a live reference the controller must exist before
// the zone can validate/apply against.
//
// The created controller is type "bgp", not "evpn": the captured
// /cluster/sdn/controllers schema (planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt) gives an "evpn"-type controller only
// fabric/peerGroupName/routeMapIn/routeMapOut — no asn/peers of its own
// (params_sdn_controller.go's doc comment) — so an "evpn" controller alone
// carries no BGP session config to establish. A "bgp"-type controller is
// what actually owns asn/peers/ebgp, which is the underlay a BGP-EVPN
// datacenter fabric needs; PVE's own EVPN zone `--controller` field accepts
// either type's id (the capture does not distinguish), and this starter
// picks the one with a session to configure. Peers is deliberately left
// unset (Required: false, no default) rather than guessed — a BGP peer
// address is site-specific in a way no starter default could stand in for,
// and an omitted field on SdnControllerCreateParams is a legal, complete
// create (PVE accepts a controller with no configured peers; it simply has
// nothing to peer with until edited).
func starterEVPNDatacenter() *Blueprint {
	return &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		ID:               StarterEVPNDatacenter,
		Name:             "EVPN datacenter starter",
		Description: "A BGP controller plus an EVPN zone/VNet/subnet for a BGP-EVPN datacenter fabric. " +
			"Creates its own controller (type bgp) rather than assuming one already exists.",
		ReadOnly:     true,
		NodeSelector: NodeSelector{Mode: SelectSingle},
		Params: []ParamDef{
			{Name: "controllerId", Type: ParamString, Label: "Controller id", Default: "evpn1", Required: true},
			{Name: "controllerAsn", Type: ParamInt, Label: "Controller ASN", Default: 65000, Required: true},
			{Name: "zoneId", Type: ParamString, Label: "Zone id", Default: "evpnzone1", Required: true},
			{Name: "vnetName", Type: ParamString, Label: "VNet name", Default: "evpnnet1", Required: true},
			{Name: "vni", Type: ParamInt, Label: "VXLAN VNI (vrfVxlan)", Default: 20000, Required: true},
			{Name: "subnetCidr", Type: ParamCIDR, Label: "Subnet", Default: "10.200.0.0/24", Required: true},
			{Name: "gateway", Type: ParamIP, Label: "Gateway", Default: "10.200.0.1",
				Required: true, AddressSuggest: true, Subnet: "10.200.0.0/24"},
		},
		Entities: []EntityTemplate{
			{
				Kind:       KindSdnController,
				IDTemplate: "{{controllerId}}",
				Fields: map[string]any{
					"type": "bgp",
					"asn":  "{{controllerAsn}}",
				},
			},
			{
				Kind:       KindSdnZone,
				IDTemplate: "{{zoneId}}",
				Fields: map[string]any{
					"type":       "evpn",
					"controller": "{{controllerId}}",
					"nodes":      "{{__nodes__}}",
					"vrfVxlan":   "{{vni}}",
				},
			},
			{
				Kind:       KindSdnVnet,
				IDTemplate: "{{zoneId}}/{{vnetName}}",
				Fields: map[string]any{
					"zone": "{{zoneId}}",
				},
			},
			{
				Kind:       KindSdnSubnet,
				IDTemplate: "{{subnetCidr}}",
				Fields: map[string]any{
					"vnet":    "{{zoneId}}/{{vnetName}}",
					"cidr":    "{{subnetCidr}}",
					"gateway": "{{gateway}}",
					"snat":    true,
				},
			},
		},
	}
}
