package pve

import (
	"context"
	"fmt"
)

// SDN Fabrics (T-3101): PVE 9's underlay-routing object family, captured
// read-only from a live PVE 9.2.4 node — planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt. A fabric is a different object family from a
// zone/vnet/subnet (this file mirrors sdn_dns.go's structure, not sdn.go's
// zone/vnet/subnet one, since a fabric has no vnet/subnet children): it
// configures underlay routing (BGP/OpenFabric/OSPF/WireGuard) a vxlan/evpn
// zone can ride on, addressed at /cluster/sdn/fabrics/fabric[/{id}], plus a
// separate cluster-wide per-node membership read at
// /cluster/sdn/fabrics/node. Like zones/vnets/subnets, writes stage a
// pending edit only — realized by the same PUT /cluster/sdn this package's
// ApplySDN already issues (internal/change/apply_sdn.go never gives fabrics
// a bespoke apply path — see planning/reports/T-3101-followup-01.md for why
// vnprox does not take the `--lock-token` this create call also accepts).
//
// The `--protocol` parameter is conditional-schema: which of the remaining
// fields are meaningful depends on the chosen protocol (openfabric:
// CSNPInterval/HelloInterval/RouteFilter; ospf: Area/Redistribute/
// RouteFilter; bgp: Redistribute; wireguard: PersistentKeepalive). This
// struct carries every field from every protocol (like SDNZone carries
// every zone type's fields) — internal/change's schema validator enforces
// which combination is actually legal for a given Protocol, and Protocol
// itself is not editable on update (unconfirmed against a `pvesh usage
// ... -v`'s `set` form — the capture has no PUT/set usage block for this
// path — so this is internal/change/params_sdn_fabric.go's documented
// assumption, not a fact read off hardware).
type SDNFabric struct {
	ID                  string       `json:"id"`
	Protocol            string       `json:"protocol"`
	Pending             PendingState `json:"pending,omitempty"`
	IPPrefix            string       `json:"ip_prefix,omitempty"`
	IP6Prefix           string       `json:"ip6_prefix,omitempty"`
	RouteFilter         string       `json:"route_filter,omitempty"`
	Area                string       `json:"area,omitempty"`
	Redistribute        []string     `json:"redistribute,omitempty"`
	CSNPInterval        int          `json:"csnp_interval,omitempty"`
	HelloInterval       int          `json:"hello_interval,omitempty"`
	PersistentKeepalive int          `json:"persistent_keepalive,omitempty"`
}

// SDNFabricNode is one row of GET /cluster/sdn/fabrics/node: one node's
// membership in one fabric, with the node-scoped IP the fabric's
// IPPrefix/IP6Prefix allocated it. Unlike GET /cluster/sdn/zones/{zone}/
// status, the capture's `ls /cluster/sdn/fabrics` shows no per-fabric
// `status` child route — this is a membership/config read, not a
// realization-health read, so internal/sdn.Service's Fabric.NodeStatus
// (built from this) can only report configured membership, not verified
// per-node health the way Zone.NodeStatus can. Field names beyond
// fabric/node are not in the capture (the fixture's fabrics were empty) —
// IP/IP6 are this package's best inference from IPPrefix/IP6Prefix's
// stated purpose ("The IP prefix for Node IPs") and are flagged in
// planning/reports/needs-hardware-validation.md pending a populated
// capture.
type SDNFabricNode struct {
	Fabric string `json:"fabric"`
	Node   string `json:"node"`
	IP     string `json:"ip,omitempty"`
	IP6    string `json:"ip6,omitempty"`
}

// ListSDNFabrics calls GET /cluster/sdn/fabrics/fabric.
func (c *Client) ListSDNFabrics(ctx context.Context) ([]SDNFabric, error) {
	var out []SDNFabric
	if err := c.do(ctx, "GET", "/cluster/sdn/fabrics/fabric", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSDNFabric calls GET /cluster/sdn/fabrics/fabric/{id}.
func (c *Client) GetSDNFabric(ctx context.Context, id string) (*SDNFabric, error) {
	var out SDNFabric
	path := fmt.Sprintf("/cluster/sdn/fabrics/fabric/%s", id)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSDNFabric calls POST /cluster/sdn/fabrics/fabric. Deliberately does
// not send --lock-token — see this file's package doc comment and
// planning/reports/T-3101-followup-01.md.
func (c *Client) CreateSDNFabric(ctx context.Context, f SDNFabric) error {
	return c.do(ctx, "POST", "/cluster/sdn/fabrics/fabric", requestParams{body: f}, nil)
}

// UpdateSDNFabric calls PUT /cluster/sdn/fabrics/fabric/{id}.
func (c *Client) UpdateSDNFabric(ctx context.Context, id string, f SDNFabric) error {
	path := fmt.Sprintf("/cluster/sdn/fabrics/fabric/%s", id)
	return c.do(ctx, "PUT", path, requestParams{body: f}, nil)
}

// DeleteSDNFabric calls DELETE /cluster/sdn/fabrics/fabric/{id}.
func (c *Client) DeleteSDNFabric(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/fabrics/fabric/%s", id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// ListSDNFabricNodes calls GET /cluster/sdn/fabrics/node: cluster-wide
// per-node fabric membership, its own collection (not nested under
// ListSDNFabrics — see this file's package doc comment).
func (c *Client) ListSDNFabricNodes(ctx context.Context) ([]SDNFabricNode, error) {
	var out []SDNFabricNode
	if err := c.do(ctx, "GET", "/cluster/sdn/fabrics/node", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// --- prefix-lists / route-maps (read-only in T-3101; see that task card's
// "Also in scope" section) -------------------------------------------------
//
// Both are live `/cluster/sdn` families the capture confirms exist
// (`ls /cluster/sdn` lists them) but for which no `pvesh usage` output was
// captured (the fixture cluster had zero of either configured, so only an
// empty `ls` was observed) — planning/reports/needs-hardware-validation.md
// flags the exact field shape below as unconfirmed. They are BGP
// route-policy objects that almost certainly couple to T-3102's SDN
// controllers (a controller's `--route-map-in`/`--route-map-out` name one
// of these); establishing that coupling with a real field shape is
// T-3102's job once a populated capture exists, not a guess this card
// makes. ID is modeled as the PVE convention every other SDN family in
// this package uses: the object's own name is both its collection-index
// key and (inferred, not captured) its "name" wire field.

// SDNPrefixList is one BGP prefix-list object
// (GET /cluster/sdn/prefix-lists). Field shape beyond ID is unconfirmed
// against hardware — see this file's package doc comment.
type SDNPrefixList struct {
	ID string `json:"name"`
}

// SDNRouteMap is one BGP route-map object (GET /cluster/sdn/route-maps).
// Field shape beyond ID is unconfirmed against hardware — see this file's
// package doc comment.
type SDNRouteMap struct {
	ID string `json:"name"`
}

// ListSDNPrefixLists calls GET /cluster/sdn/prefix-lists. Read-only: no
// create/update/delete methods exist in this package (T-3101 scopes CRUD
// out — see internal/change's guard test proving no sdn.prefix-list.* op
// exists).
func (c *Client) ListSDNPrefixLists(ctx context.Context) ([]SDNPrefixList, error) {
	var out []SDNPrefixList
	if err := c.do(ctx, "GET", "/cluster/sdn/prefix-lists", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNRouteMaps calls GET /cluster/sdn/route-maps. Read-only — see
// ListSDNPrefixLists's doc comment.
func (c *Client) ListSDNRouteMaps(ctx context.Context) ([]SDNRouteMap, error) {
	var out []SDNRouteMap
	if err := c.do(ctx, "GET", "/cluster/sdn/route-maps", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
