package change

// VlanCreateParams is op "vlan.create". Target carries the new VLAN
// sub-interface's identity (e.g. Ref{Kind: KindVlan, Node: "pve1", ID:
// "vmbr0.20"}); Parent names the parent interface (by name — always the
// same node as the target).
type VlanCreateParams struct {
	Parent    string   `json:"parent"`
	Addresses []string `json:"addresses,omitempty"`
	Vid       int      `json:"vid"`
	MTU       int      `json:"mtu,omitempty"`
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
