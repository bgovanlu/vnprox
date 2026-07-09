package change

// BondCreateParams is op "bond.create". Target carries the new bond's
// identity (e.g. Ref{Kind: KindBond, Node: "pve1", ID: "bond0"}); these
// params carry everything else docs/data-model.md's Bond entity documents
// as declared (not runtime-only) config.
type BondCreateParams struct {
	Mode           string   `json:"mode"`
	LACPRate       string   `json:"lacpRate,omitempty"`
	XmitHashPolicy string   `json:"xmitHashPolicy,omitempty"`
	Comments       string   `json:"comments,omitempty"`
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
