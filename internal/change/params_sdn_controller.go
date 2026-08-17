package change

// T-3102 SDN Controller op params, mirroring params_sdn_fabric.go's
// SdnFabric*Params field-set/doc-comment density and its pointer-field
// convention for Update. A controller's Target carries its cluster-scoped
// identity (Ref{Kind: KindSDNController, ID: "<controllerID>"}, id
// `[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]` per the captured `--controller`
// pattern — planning/reports/evidence/pve-9.2.4-sdn-schema.txt).
//
// Type is conditional-schema, the same "one struct, schema-validated
// combination" choice params_sdn_fabric.go documents and SdnZoneCreateParams
// already makes across five zone types: rather than splitting into four
// type-specific params types, this carries every type's fields and lets
// validate_schema.go's conditional arm enforce which combination is legal
// for a given Type — symmetric with what pvemock's own
// sdnControllerTypeError enforces server-side
// (internal/pvemock/sdn_controller.go). Unlike the fabric capture, the
// controller capture has no explicit "Conditional options:" grouping, so
// which fields belong to which type here is inferred from each field's own
// description (see internal/pve/sdn_controller.go's package doc comment for
// the same caveat, flagged in needs-hardware-validation.md): bgp gets
// asn/bgpMode/bgpMultipathAsPathRelax/ebgp/ebgpMultihop/peers; evpn gets
// fabric/peerGroupName/routeMapIn/routeMapOut; isis gets
// isisDomain/isisIfaces/isisNet; faucet gets none beyond the fields every
// type carries (node/nodes/loopback).
type SdnControllerCreateParams struct {
	Type          string `json:"type"`
	BgpMode       string `json:"bgpMode,omitempty"`
	Fabric        string `json:"fabric,omitempty"`
	IsisDomain    string `json:"isisDomain,omitempty"`
	IsisNet       string `json:"isisNet,omitempty"`
	Loopback      string `json:"loopback,omitempty"`
	Node          string `json:"node,omitempty"`
	PeerGroupName string `json:"peerGroupName,omitempty"`
	RouteMapIn    string `json:"routeMapIn,omitempty"`
	RouteMapOut   string `json:"routeMapOut,omitempty"`

	Nodes      []string `json:"nodes,omitempty"`
	Peers      []string `json:"peers,omitempty"`
	IsisIfaces []string `json:"isisIfaces,omitempty"`

	ASN                     int  `json:"asn,omitempty"`
	EbgpMultihop            int  `json:"ebgpMultihop,omitempty"`
	Ebgp                    bool `json:"ebgp,omitempty"`
	BgpMultipathAsPathRelax bool `json:"bgpMultipathAsPathRelax,omitempty"`
}

func (SdnControllerCreateParams) isChangeParams() {}

// SdnControllerUpdateParams is op "sdn.controller.update": a partial
// update. Type is NOT editable — the same reasoning SdnFabricUpdateParams'
// doc comment gives for Protocol (unconfirmed against a `pvesh usage ...
// -v`'s `set` form; the capture has no PUT/set usage block for this path
// either), reinforced here by the fact that changing Type would change
// which of every other field is even legal, exactly like a zone's own Type
// immutability (create+delete already required to change a zone's type).
type SdnControllerUpdateParams struct {
	BgpMode       *string `json:"bgpMode,omitempty"`
	Fabric        *string `json:"fabric,omitempty"`
	IsisDomain    *string `json:"isisDomain,omitempty"`
	IsisNet       *string `json:"isisNet,omitempty"`
	Loopback      *string `json:"loopback,omitempty"`
	Node          *string `json:"node,omitempty"`
	PeerGroupName *string `json:"peerGroupName,omitempty"`
	RouteMapIn    *string `json:"routeMapIn,omitempty"`
	RouteMapOut   *string `json:"routeMapOut,omitempty"`

	Nodes      *[]string `json:"nodes,omitempty"`
	Peers      *[]string `json:"peers,omitempty"`
	IsisIfaces *[]string `json:"isisIfaces,omitempty"`

	ASN                     *int  `json:"asn,omitempty"`
	EbgpMultihop            *int  `json:"ebgpMultihop,omitempty"`
	Ebgp                    *bool `json:"ebgp,omitempty"`
	BgpMultipathAsPathRelax *bool `json:"bgpMultipathAsPathRelax,omitempty"`
}

func (SdnControllerUpdateParams) isChangeParams() {}

// SdnControllerDeleteParams is op "sdn.controller.delete".
type SdnControllerDeleteParams struct{}

func (SdnControllerDeleteParams) isChangeParams() {}
