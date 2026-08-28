// SPDX-License-Identifier: Apache-2.0

package change

// BondCreateParams is op "bond.create". Target carries the new bond's
// identity (Kind is KindBond for a Linux bond or KindOVSBond for an OVS
// one — Kind alone disambiguates most fields, same as BridgeCreateParams).
// These params carry everything else docs/data-model.md's Bond entity
// documents as declared (not runtime-only) config.
//
// Bridge is OVS-only: the name of the OVS bridge this bond attaches to
// (rendered as ovs_bridge — see internal/change/ifaces.BondCreate's doc
// comment). It is required when Target.Kind is KindOVSBond and ignored
// otherwise ("params carry ovs-specific fields", per docs/data-model.md).
type BondCreateParams struct {
	Mode           string   `json:"mode"`
	LACPRate       string   `json:"lacpRate,omitempty"`
	XmitHashPolicy string   `json:"xmitHashPolicy,omitempty"`
	Comments       string   `json:"comments,omitempty"`
	Bridge         string   `json:"bridge,omitempty"`
	Slaves         []string `json:"slaves"`
	MIIMon         int      `json:"miimon,omitempty"`
	MTU            int      `json:"mtu,omitempty"`
}

func (BondCreateParams) isChangeParams() {}

// BondUpdateParams is op "bond.update": a partial update of an existing
// bond's declared config. See IfaceUpdateParams' doc comment for the
// pointer-field convention.
type BondUpdateParams struct {
	Mode           *string   `json:"mode,omitempty"`
	Slaves         *[]string `json:"slaves,omitempty"`
	LACPRate       *string   `json:"lacpRate,omitempty"`
	XmitHashPolicy *string   `json:"xmitHashPolicy,omitempty"`
	MIIMon         *int      `json:"miimon,omitempty"`
	MTU            *int      `json:"mtu,omitempty"`
	Comments       *string   `json:"comments,omitempty"`
}

func (BondUpdateParams) isChangeParams() {}

// BondDeleteParams is op "bond.delete". It takes no parameters — the
// target Ref alone identifies the bond to remove — but is still a
// distinct, strictly-decoded type so an accidental/unexpected field on a
// delete op is rejected rather than silently ignored.
type BondDeleteParams struct{}

func (BondDeleteParams) isChangeParams() {}
