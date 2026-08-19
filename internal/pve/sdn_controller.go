package pve

import (
	"context"
	"encoding/json"
	"fmt"
)

// SDN Controllers (T-3102): PVE's underlay-control-plane object family,
// captured read-only from a live PVE 9.2.4 node — planning/reports/
// evidence/pve-9.2.4-sdn-schema.txt. A controller predates T-3102 as a bare
// string on a zone's `--controller` field (sdn.go's SDNZone.Controller,
// which stays a *reference* by id — unchanged by this file); this file adds
// the controller object itself as a first-class read/write family, mirroring
// sdn_fabric.go's structure (this package's closest precedent: a different
// object family from a zone/vnet/subnet, addressed at its own top-level
// /cluster/sdn/controllers[/{id}] path, writes stage a pending edit only,
// realized by the same PUT /cluster/sdn ApplySDN already issues —
// internal/change/apply_sdn.go never gives controllers a bespoke apply
// path, for the same `--lock-token` reason fabrics don't — see
// planning/reports/T-3101-followup-01.md).
//
// The capture's four types (`--type <bgp | evpn | faucet | isis>`) each use
// a different subset of the fields below; this struct carries every field
// from every type (like SDNFabric carries every protocol's fields) —
// internal/change's schema validator enforces which combination is legal
// for a given Type. Unlike the fabric capture, this one has no
// "Conditional options:" grouping in the `pvesh usage` transcript, so the
// per-type field assignment below is inferred from each field's own
// description rather than read directly off a grouped block — flagged in
// planning/reports/needs-hardware-validation.md pending a capture that
// exercises `pvesh usage /cluster/sdn/controllers -v` against a
// non-empty/populated set of each type.
type SDNController struct {
	ID            string `json:"controller"`
	Type          string `json:"type"`
	BgpMode       string `json:"bgp-mode,omitempty"`
	Fabric        string `json:"fabric,omitempty"`
	IsisDomain    string `json:"isis-domain,omitempty"`
	IsisNet       string `json:"isis-net,omitempty"`
	Loopback      string `json:"loopback,omitempty"`
	Node          string `json:"node,omitempty"`
	PeerGroupName string `json:"peer-group-name,omitempty"`
	RouteMapIn    string `json:"route-map-in,omitempty"`
	RouteMapOut   string `json:"route-map-out,omitempty"`
	// Pending decodes whatever "pending" key this struct's DEFAULT list/get
	// view (no query param) returns — which, against real PVE 9.2.4, is
	// none: the same T-401-era trap SDNZone.Pending's doc comment describes
	// in full (sdn.go), confirmed here too directly against pvecube's own
	// perl source rather than merely assumed from that precedent
	// (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt §6).
	// Callers that need real foreign-pending detection must call
	// ListSDNControllersPending (this file's "?pending=1" view) instead.
	// Kept (not removed) for the same reason SDNZone.Pending is: pvemock's
	// own default view still populates it, and other packages' tests still
	// exercise it directly.
	Pending PendingState `json:"pending,omitempty"`
	Nodes   []string     `json:"nodes,omitempty"`
	Peers   []string     `json:"peers,omitempty"`
	// IsisIfaces is real PVE's "Comma-separated list of interfaces" field
	// (isis-only); modeled as []string like Nodes/Peers above rather than
	// the single un-split string the capture's description literally says,
	// for the same reason Nodes/Peers already are — a caller-facing list is
	// more useful than a caller-facing comma string, and the wire
	// conversion is symmetric with commaList's existing convention.
	IsisIfaces              []string `json:"isis-ifaces,omitempty"`
	ASN                     int      `json:"asn,omitempty"`
	EbgpMultihop            int      `json:"ebgp-multihop,omitempty"`
	Ebgp                    bool     `json:"ebgp,omitempty"`
	BgpMultipathAsPathRelax bool     `json:"bgp-multipath-as-path-relax,omitempty"`
}

// MarshalJSON/UnmarshalJSON translate SDNController's comma-list fields
// (nodes/peers/isis-ifaces) and numeric-boolean fields (ebgp/
// bgp-multipath-as-path-relax) to and from PVE's own wire conventions — see
// commaList's and pveBool's doc comments (sdn_commalist.go/pvebool.go),
// reused verbatim rather than forked, the same way SDNZone's own
// MarshalJSON/UnmarshalJSON already do for its own comma-list fields.
func (c SDNController) MarshalJSON() ([]byte, error) {
	type alias SDNController
	return json.Marshal(struct {
		Nodes      commaList `json:"nodes,omitempty"`
		Peers      commaList `json:"peers,omitempty"`
		IsisIfaces commaList `json:"isis-ifaces,omitempty"`
		alias
		Ebgp                    pveBool `json:"ebgp,omitempty"`
		BgpMultipathAsPathRelax pveBool `json:"bgp-multipath-as-path-relax,omitempty"`
	}{
		Nodes: commaList(c.Nodes), Peers: commaList(c.Peers), IsisIfaces: commaList(c.IsisIfaces),
		alias:                   alias(c),
		Ebgp:                    pveBool(c.Ebgp),
		BgpMultipathAsPathRelax: pveBool(c.BgpMultipathAsPathRelax),
	})
}

func (c *SDNController) UnmarshalJSON(data []byte) error {
	type alias SDNController
	aux := struct {
		*alias
		Nodes      commaList `json:"nodes,omitempty"`
		Peers      commaList `json:"peers,omitempty"`
		IsisIfaces commaList `json:"isis-ifaces,omitempty"`

		Ebgp                    pveBool `json:"ebgp,omitempty"`
		BgpMultipathAsPathRelax pveBool `json:"bgp-multipath-as-path-relax,omitempty"`
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	c.Nodes, c.Peers, c.IsisIfaces = aux.Nodes, aux.Peers, aux.IsisIfaces
	c.Ebgp, c.BgpMultipathAsPathRelax = bool(aux.Ebgp), bool(aux.BgpMultipathAsPathRelax)
	return nil
}

// ListSDNControllers calls GET /cluster/sdn/controllers.
func (c *Client) ListSDNControllers(ctx context.Context) ([]SDNController, error) {
	var out []SDNController
	if err := c.do(ctx, "GET", "/cluster/sdn/controllers", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNControllersPending calls GET /cluster/sdn/controllers?pending=1 —
// see sdn.go's pendingQuery doc comment for the general "?pending=1"
// mechanism. Added by the debt-sweep 2026-08-19 follow-up ("SDNController.
// Pending and SDNFabric.Pending have the same gap [as SDNZone.Pending]"):
// SDNController.Pending above has the identical T-401-era defect
// SDNZone.Pending does (decodes a "pending" key the DEFAULT list view never
// actually carries against real PVE) — confirmed directly against pvecube's
// own perl source, not merely assumed from the zone/vnet/subnet precedent
// (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt §6):
// PVE::API2::Network::SDN::Controllers.pm's GET handler calls the exact
// same PVE::Network::SDN::pending_config($running_cfg, $config,
// 'controllers') zones/vnets/subnets already use. Callers needing real
// foreign-pending detection for a controller must call this instead of
// reading SDNController.Pending off ListSDNControllers.
func (c *Client) ListSDNControllersPending(ctx context.Context) ([]SDNPendingEntry, error) {
	var out []sdnPendingWire
	if err := c.do(ctx, "GET", "/cluster/sdn/controllers", requestParams{query: pendingQuery}, &out); err != nil {
		return nil, err
	}
	return sdnPendingEntries("controller", func(w sdnPendingWire) string { return w.Controller }, out), nil
}

// GetSDNController calls GET /cluster/sdn/controllers/{id}.
func (c *Client) GetSDNController(ctx context.Context, id string) (*SDNController, error) {
	var out SDNController
	path := fmt.Sprintf("/cluster/sdn/controllers/%s", id)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSDNController calls POST /cluster/sdn/controllers. Deliberately
// does not send --lock-token — see this file's package doc comment and
// planning/reports/T-3101-followup-01.md.
func (c *Client) CreateSDNController(ctx context.Context, ctl SDNController) error {
	return c.do(ctx, "POST", "/cluster/sdn/controllers", requestParams{body: ctl}, nil)
}

// UpdateSDNController calls PUT /cluster/sdn/controllers/{id}.
func (c *Client) UpdateSDNController(ctx context.Context, id string, ctl SDNController) error {
	path := fmt.Sprintf("/cluster/sdn/controllers/%s", id)
	return c.do(ctx, "PUT", path, requestParams{body: ctl}, nil)
}

// DeleteSDNController calls DELETE /cluster/sdn/controllers/{id}.
func (c *Client) DeleteSDNController(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/controllers/%s", id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}
