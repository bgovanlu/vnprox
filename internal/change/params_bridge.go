// SPDX-License-Identifier: Apache-2.0

package change

// BridgeCreateParams is op "bridge.create". Target carries the new
// bridge's identity (Kind is KindBridge for a Linux bridge or
// KindOVSBridge for an OVS one — Kind alone disambiguates, so there is no
// separate "kind" field here duplicating it).
type BridgeCreateParams struct {
	Gateway   string     `json:"gateway,omitempty"`
	Comments  string     `json:"comments,omitempty"`
	Ports     []string   `json:"ports,omitempty"`
	Vids      []VidRange `json:"vids,omitempty"`
	Addresses []string   `json:"addresses,omitempty"`
	MTU       int        `json:"mtu,omitempty"`
	VlanAware bool       `json:"vlanAware,omitempty"`
	STP       bool       `json:"stp,omitempty"`
}

func (BridgeCreateParams) isChangeParams() {}

// BridgeUpdateParams is op "bridge.update": a partial update of an
// existing bridge's declared config. Port membership changes go through
// the dedicated bridge.port.add/remove ops instead (docs/data-model.md §3)
// rather than a Ports field here, so a bridge.update can never accidentally
// clobber port membership a concurrent op in the same changeset is also
// touching.
type BridgeUpdateParams struct {
	VlanAware *bool       `json:"vlanAware,omitempty"`
	Vids      *[]VidRange `json:"vids,omitempty"`
	Addresses *[]string   `json:"addresses,omitempty"`
	Gateway   *string     `json:"gateway,omitempty"`
	MTU       *int        `json:"mtu,omitempty"`
	STP       *bool       `json:"stp,omitempty"`
	Comments  *string     `json:"comments,omitempty"`
}

func (BridgeUpdateParams) isChangeParams() {}

// BridgeDeleteParams is op "bridge.delete". Per docs/features/
// change-management.md §5, deleting a bridge with attached guests requires
// the same changeset to also reattach every one (generated as separate
// guest.nic.update ops) — that safety interlock is T-203/T-202's
// validation-time job, not something this delete op's params need to
// carry.
type BridgeDeleteParams struct{}

func (BridgeDeleteParams) isChangeParams() {}

// BridgePortAddParams is op "bridge.port.add": target is the bridge, Port
// names the physnic/bond/vlan-iface to enslave (by name, not a full Ref —
// it is always on the same node as the bridge).
type BridgePortAddParams struct {
	Port string `json:"port"`
}

func (BridgePortAddParams) isChangeParams() {}

// BridgePortRemoveParams is op "bridge.port.remove": the inverse of
// BridgePortAddParams.
type BridgePortRemoveParams struct {
	Port string `json:"port"`
}

func (BridgePortRemoveParams) isChangeParams() {}
