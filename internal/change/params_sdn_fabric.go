package change

// T-3101 SDN Fabric op params, mirroring params_sdn.go's SdnZone*Params
// field-set/doc-comment density and its pointer-field convention for
// Update. A fabric's Target carries its cluster-scoped identity
// (Ref{Kind: KindSDNFabric, ID: "<fabricID>"}, ID 2-8 chars per the
// captured `--id` pattern — planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt).
//
// Protocol is conditional-schema (the capture's "Conditional options:"
// blocks): which of CSNPInterval/HelloInterval/RouteFilter/Area/
// Redistribute/PersistentKeepalive are meaningful depends on Protocol.
// Rather than splitting into four protocol-specific params types, this
// carries every field (like pve.SDNFabric does at the wire boundary) and
// lets validate_schema.go's conditional arm enforce which combination is
// legal for a given Protocol — the same "one struct, schema-validated
// combination" choice SdnZoneCreateParams already makes across five zone
// types' worth of fields (bridge/controller/vrfVxlan are only meaningful
// for some zone types too, and nothing splits SdnZoneCreateParams over
// that). Symmetric with what pvemock's own sdnFabricProtocolError enforces
// server-side (internal/pvemock/sdn_fabric.go) so the two can never quietly
// disagree about which combination is legal.

// SdnFabricCreateParams is op "sdn.fabric.create".
type SdnFabricCreateParams struct {
	Protocol            string   `json:"protocol"`
	IPPrefix            string   `json:"ipPrefix,omitempty"`
	IP6Prefix           string   `json:"ip6Prefix,omitempty"`
	RouteFilter         string   `json:"routeFilter,omitempty"`
	Area                string   `json:"area,omitempty"`
	Redistribute        []string `json:"redistribute,omitempty"`
	CSNPInterval        int      `json:"csnpInterval,omitempty"`
	HelloInterval       int      `json:"helloInterval,omitempty"`
	PersistentKeepalive int      `json:"persistentKeepalive,omitempty"`
}

func (SdnFabricCreateParams) isChangeParams() {}

// SdnFabricUpdateParams is op "sdn.fabric.update": a partial update.
// Protocol is NOT editable — an assumption, not a confirmed fact: the
// capture (planning/reports/evidence/pve-9.2.4-sdn-schema.txt) has no
// `pvesh usage /cluster/sdn/fabrics/fabric -v`'s `set` form at all, only
// `get`/`create`, so whether real PVE's own PUT even allows changing
// protocol is unconfirmed. This mirrors SdnZoneUpdateParams' Type
// immutability (create+delete already required to change a zone's type)
// on the same reasoning PVE's own conditional-per-protocol schema implies
// — changing protocol changes which of every other field is even legal —
// pending hardware validation of the real `set` usage block.
type SdnFabricUpdateParams struct {
	IPPrefix            *string   `json:"ipPrefix,omitempty"`
	IP6Prefix           *string   `json:"ip6Prefix,omitempty"`
	CSNPInterval        *int      `json:"csnpInterval,omitempty"`
	HelloInterval       *int      `json:"helloInterval,omitempty"`
	RouteFilter         *string   `json:"routeFilter,omitempty"`
	Area                *string   `json:"area,omitempty"`
	Redistribute        *[]string `json:"redistribute,omitempty"`
	PersistentKeepalive *int      `json:"persistentKeepalive,omitempty"`
}

func (SdnFabricUpdateParams) isChangeParams() {}

// SdnFabricDeleteParams is op "sdn.fabric.delete".
type SdnFabricDeleteParams struct{}

func (SdnFabricDeleteParams) isChangeParams() {}
