package change

// NatMasqueradeCreateParams is op "nat.masquerade.create" (T-1403). Target
// carries the new rule's identity (Ref{Kind: KindNatRule, ID:
// <caller-chosen id>}) — the rule has no interfaces(5) stanza of its own;
// Iface names the *existing* stanza (typically the uplink/WAN-facing
// iface) the generated post-up/post-down MASQUERADE lines attach to.
type NatMasqueradeCreateParams struct {
	Iface      string `json:"iface"`
	SourceCIDR string `json:"sourceCidr"`
	Comment    string `json:"comment,omitempty"`
}

func (NatMasqueradeCreateParams) isChangeParams() {}

// NatMasqueradeDeleteParams is op "nat.masquerade.delete". There is no
// nat.masquerade.update op: changing a masquerade rule's shape is
// delete-and-recreate, so it is always visible as two ordinary audited
// changeset ops, never a silent in-place overwrite (mirrors T-1401's
// key-rotation convention for the identical reason).
type NatMasqueradeDeleteParams struct{}

func (NatMasqueradeDeleteParams) isChangeParams() {}

// NatPortForwardCreateParams is op "nat.portforward.create": a DNAT rule
// forwarding ExtPort/Proto arriving on Iface to IntIP:IntPort.
type NatPortForwardCreateParams struct {
	Iface   string `json:"iface"`
	Proto   string `json:"proto"` // tcp|udp
	IntIP   string `json:"intIp"`
	Comment string `json:"comment,omitempty"`
	ExtPort int    `json:"extPort"`
	IntPort int    `json:"intPort"`
}

func (NatPortForwardCreateParams) isChangeParams() {}

// NatPortForwardUpdateParams is op "nat.portforward.update": a partial
// patch of the rule Target names — every non-nil field replaces the
// currently stored value (host.NatPortForwardConfig, recovered from the
// rule's own generated marker at apply time — see edgeop.go), nil fields
// keep it unchanged.
type NatPortForwardUpdateParams struct {
	Iface   *string `json:"iface,omitempty"`
	Proto   *string `json:"proto,omitempty"`
	IntIP   *string `json:"intIp,omitempty"`
	Comment *string `json:"comment,omitempty"`
	ExtPort *int    `json:"extPort,omitempty"`
	IntPort *int    `json:"intPort,omitempty"`
}

func (NatPortForwardUpdateParams) isChangeParams() {}

// NatPortForwardDeleteParams is op "nat.portforward.delete".
type NatPortForwardDeleteParams struct{}

func (NatPortForwardDeleteParams) isChangeParams() {}

// RouteStaticCreateParams is op "route.static.create" (T-1403): an
// additional/policy static route (DestCIDR via Gateway, dev Iface). A
// node's *default* gateway stays owned by iface.update's own gateway field
// (docs/data-model.md §3) — this op group never sets it, only additional
// routes alongside it.
type RouteStaticCreateParams struct {
	Iface    string `json:"iface"`
	DestCIDR string `json:"destCidr"`
	Gateway  string `json:"gateway"`
	Comment  string `json:"comment,omitempty"`
	Metric   int    `json:"metric,omitempty"`
}

func (RouteStaticCreateParams) isChangeParams() {}

// RouteStaticUpdateParams is op "route.static.update": a partial patch,
// same semantics as NatPortForwardUpdateParams.
type RouteStaticUpdateParams struct {
	Iface    *string `json:"iface,omitempty"`
	DestCIDR *string `json:"destCidr,omitempty"`
	Gateway  *string `json:"gateway,omitempty"`
	Comment  *string `json:"comment,omitempty"`
	Metric   *int    `json:"metric,omitempty"`
}

func (RouteStaticUpdateParams) isChangeParams() {}

// RouteStaticDeleteParams is op "route.static.delete".
type RouteStaticDeleteParams struct{}

func (RouteStaticDeleteParams) isChangeParams() {}
