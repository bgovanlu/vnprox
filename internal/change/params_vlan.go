// SPDX-License-Identifier: Apache-2.0

package change

// VlanCreateParams is op "vlan.create". Target carries the new VLAN
// sub-interface's identity (e.g. Ref{Kind: KindVlan, Node: "pve1", ID:
// "vmbr0.20"}); Parent names the parent interface (by name — always the
// same node as the target).
//
// OVS marks this create as an OVS Int Port instead of a plain 802.1q
// sub-interface (docs/data-model.md: KindVlan has no dedicated OVS-kind
// sibling the way Bridge/Bond do, so — per the data model's "params carry
// ovs-specific fields" note — the distinction lives here, not on Target.Kind).
// When OVS is true, Parent must name an existing OVS bridge (ovs_bridge),
// Vid becomes the OVS access "tag" (0 = untagged/native), and Trunks is an
// optional set of additional trunked VLAN ranges (ovs-vsctl's Port "trunks"
// column) — both may be set together (OVS's native-tagged/native-untagged
// vlan_mode use case) or Vid may be 0 with only Trunks set (a pure trunk
// port). Trunks is rejected when OVS is false: a plain 802.1q sub-interface
// always carries exactly one VID (Vid itself).
// Gateway (T-703) is the sub-interface's default gateway, rendered as the
// stanza's `gateway` option exactly like BridgeCreateParams.Gateway — added
// for the dedicated-management-VLAN flow (docs/features/change-management.md
// §5's VLAN editor never needed it, but a VLAN sub-interface *carrying the
// node's management IP* must also carry the node's default route, or moving
// management onto it silently strands off-subnet connectivity).
type VlanCreateParams struct {
	Parent    string     `json:"parent"`
	Gateway   string     `json:"gateway,omitempty"`
	Addresses []string   `json:"addresses,omitempty"`
	Trunks    []VidRange `json:"trunks,omitempty"`
	Vid       int        `json:"vid"`
	MTU       int        `json:"mtu,omitempty"`
	OVS       bool       `json:"ovs,omitempty"`
}

func (VlanCreateParams) isChangeParams() {}

// VlanUpdateParams is op "vlan.update": a partial update. Parent/Vid are
// intentionally not editable here — changing either is a different
// interface identity in practice (docs/features/change-management.md §5
// scopes VLAN editing to parent picker/VID/addresses/MTU at *create* time;
// re-parenting or re-tagging an existing VLAN iface is a delete+create,
// same as physical NIC renames being out of v1 scope for the same reason).
type VlanUpdateParams struct {
	Addresses *[]string `json:"addresses,omitempty"`
	MTU       *int      `json:"mtu,omitempty"`
}

func (VlanUpdateParams) isChangeParams() {}

// VlanDeleteParams is op "vlan.delete".
type VlanDeleteParams struct{}

func (VlanDeleteParams) isChangeParams() {}
