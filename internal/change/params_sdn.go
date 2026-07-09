package change

// SdnZoneCreateParams is op "sdn.zone.create". Target carries the new
// zone's cluster-scoped identity (Ref{Kind: KindSDNZone, Node: "", ID:
// "zone1"}).
type SdnZoneCreateParams struct {
	Type       string   `json:"type"` // simple|vlan|qinq|vxlan|evpn
	Bridge     string   `json:"bridge,omitempty"`
	Controller string   `json:"controller,omitempty"`
	IPAM       string   `json:"ipam,omitempty"`
	Nodes      []string `json:"nodes,omitempty"`
	VrfVxlan   int      `json:"vrfVxlan,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
}

func (SdnZoneCreateParams) isChangeParams() {}

// SdnZoneUpdateParams is op "sdn.zone.update": a partial update. Type is
// not editable (changing zone type is a delete+create in real PVE too).
type SdnZoneUpdateParams struct {
	Bridge     *string   `json:"bridge,omitempty"`
	Controller *string   `json:"controller,omitempty"`
	IPAM       *string   `json:"ipam,omitempty"`
	Nodes      *[]string `json:"nodes,omitempty"`
	VrfVxlan   *int      `json:"vrfVxlan,omitempty"`
	MTU        *int      `json:"mtu,omitempty"`
}

func (SdnZoneUpdateParams) isChangeParams() {}

// SdnZoneDeleteParams is op "sdn.zone.delete".
type SdnZoneDeleteParams struct{}

func (SdnZoneDeleteParams) isChangeParams() {}

// SdnVnetCreateParams is op "sdn.vnet.create". Target carries the new
// vnet's identity (Ref{Kind: KindSDNVnet, ID: "zone1/vnet1"}); Zone is kept
// as an explicit field alongside the target's own zone-prefixed ID
// (slightly redundant, but it keeps referential validators — T-202 — from
// having to parse the target ID's "zone/vnet" convention back apart).
type SdnVnetCreateParams struct {
	Zone      string `json:"zone"`
	Alias     string `json:"alias,omitempty"`
	Tag       int    `json:"tag,omitempty"`
	VlanAware bool   `json:"vlanAware,omitempty"`
}

func (SdnVnetCreateParams) isChangeParams() {}

// SdnVnetUpdateParams is op "sdn.vnet.update": a partial update.
type SdnVnetUpdateParams struct {
	Alias     *string `json:"alias,omitempty"`
	Tag       *int    `json:"tag,omitempty"`
	VlanAware *bool   `json:"vlanAware,omitempty"`
}

func (SdnVnetUpdateParams) isChangeParams() {}

// SdnVnetDeleteParams is op "sdn.vnet.delete".
type SdnVnetDeleteParams struct{}

func (SdnVnetDeleteParams) isChangeParams() {}

// SdnSubnetCreateParams is op "sdn.subnet.create". Target carries the new
// subnet's identity (Ref{Kind: KindSDNSubnet, ID: <cidr>} — the ID *is*
// the CIDR per docs/data-model.md's SdnSubnet.ID doc comment); CIDR is
// kept as an explicit field too for the same reason as SdnVnetCreateParams'
// Zone field.
type SdnSubnetCreateParams struct {
	Vnet          string   `json:"vnet"`
	CIDR          string   `json:"cidr"`
	Gateway       string   `json:"gateway,omitempty"`
	DNSZonePrefix string   `json:"dnsZonePrefix,omitempty"`
	DHCPRanges    []string `json:"dhcpRanges,omitempty"`
	SNAT          bool     `json:"snat,omitempty"`
}

func (SdnSubnetCreateParams) isChangeParams() {}

// SdnSubnetUpdateParams is op "sdn.subnet.update": a partial update. Vnet/
// CIDR are not editable (moving a subnet to a different vnet or resizing
// its CIDR is a delete+create, since it changes which allocations are even
// valid).
type SdnSubnetUpdateParams struct {
	Gateway       *string   `json:"gateway,omitempty"`
	DNSZonePrefix *string   `json:"dnsZonePrefix,omitempty"`
	DHCPRanges    *[]string `json:"dhcpRanges,omitempty"`
	SNAT          *bool     `json:"snat,omitempty"`
}

func (SdnSubnetUpdateParams) isChangeParams() {}

// SdnSubnetDeleteParams is op "sdn.subnet.delete".
type SdnSubnetDeleteParams struct{}

func (SdnSubnetDeleteParams) isChangeParams() {}

// SdnApplyParams is op "sdn.apply": applies pending SDN configuration
// cluster-wide (docs/architecture.md §4: "an SDN apply step where
// needed"). It has no target (see noTargetOps in op.go) and no params —
// it is always the last step of a plan when any sdn.* op is present
// (docs/data-model.md §3).
type SdnApplyParams struct{}

func (SdnApplyParams) isChangeParams() {}
