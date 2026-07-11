package pve

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// SDNSubnetID derives a subnet's PVE-facing identifier from its CIDR:
// real PVE (and internal/pvemock, mirroring it — see that package's
// sdn_test.go/three-node-vlan.yaml fixture, whose declared subnet ids are
// exactly their cidr with "/" replaced by "-") uses this form as the
// {subnet} URL path segment, since a literal "/" can't appear in one path
// segment. docs/data-model.md's SdnSubnet.ID/internal/change's op Target.ID
// convention is the literal CIDR (slash form) throughout — every write
// call in this file that addresses a subnet by id (Update/DeleteSDNSubnet,
// and the "subnet" field CreateSDNSubnet's body carries) needs this
// conversion at the wire boundary; callers holding a CIDR never need to
// call it directly except when constructing the write request itself.
func SDNSubnetID(cidr string) string {
	return strings.ReplaceAll(cidr, "/", "-")
}

// SDNVnetID derives a vnet's PVE-facing identifier (the bare vnet name, as
// used both for the "vnet" wire field and the {vnet} URL path segment
// throughout this file) from internal/change's own Ref.ID convention for
// sdn-vnet targets, "<zone>/<vnet>" (params_sdn.go's SdnVnetCreateParams
// doc comment: "Target carries the new vnet's identity ... zone1/vnet1").
// Real PVE vnet names are already cluster-globally unique on their own (no
// zone prefix in the actual API) and, like SDNSubnetID's CIDR, a literal
// "/" cannot appear in a single URL path segment — so every write call
// addressing a vnet by id must convert through here first. Returns refID
// unchanged if it carries no "/" (a vnet id vnprox already received in
// bare PVE form, e.g. read back from ListSDNVnets before this package
// reconstructs the zone-prefixed form for SDNConfig).
func SDNVnetID(refID string) string {
	if i := strings.LastIndexByte(refID, '/'); i >= 0 {
		return refID[i+1:]
	}
	return refID
}

// runningQuery is the query string for the "?running=1" view real PVE (and
// internal/pvemock) serve alongside the default staged/pending-merged view
// on the SDN zones/vnets/subnets list endpoints — the last-applied
// (running) config, as opposed to the default response's staged-with-
// pending-markers view (docs/features/sdn.md §1: "vnprox surfaces
// staged-vs-running as a first-class diff"). Not part of the original
// docs/api.md contract; documented here and in docs/api.md itself per
// docs/development.md's definition-of-done #4 (added by T-401).
var runningQuery = url.Values{"running": {"1"}}

// ListSDNZones calls GET /cluster/sdn/zones.
func (c *Client) ListSDNZones(ctx context.Context) ([]SDNZone, error) {
	var out []SDNZone
	if err := c.do(ctx, "GET", "/cluster/sdn/zones", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNZonesRunning calls GET /cluster/sdn/zones?running=1: the
// last-applied config, for comparison against ListSDNZones' staged view.
func (c *Client) ListSDNZonesRunning(ctx context.Context) ([]SDNZone, error) {
	var out []SDNZone
	if err := c.do(ctx, "GET", "/cluster/sdn/zones", requestParams{query: runningQuery}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSDNZone calls GET /cluster/sdn/zones/{zone}.
func (c *Client) GetSDNZone(ctx context.Context, zone string) (*SDNZone, error) {
	var out SDNZone
	path := fmt.Sprintf("/cluster/sdn/zones/%s", zone)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSDNZoneStatus calls GET /cluster/sdn/zones/{zone}/status: per-node
// apply/health status for the zone.
func (c *Client) GetSDNZoneStatus(ctx context.Context, zone string) ([]SDNZoneStatus, error) {
	var out []SDNZoneStatus
	path := fmt.Sprintf("/cluster/sdn/zones/%s/status", zone)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNVnets calls GET /cluster/sdn/vnets.
func (c *Client) ListSDNVnets(ctx context.Context) ([]SDNVnet, error) {
	var out []SDNVnet
	if err := c.do(ctx, "GET", "/cluster/sdn/vnets", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNVnetsRunning calls GET /cluster/sdn/vnets?running=1: see
// ListSDNZonesRunning's doc comment.
func (c *Client) ListSDNVnetsRunning(ctx context.Context) ([]SDNVnet, error) {
	var out []SDNVnet
	if err := c.do(ctx, "GET", "/cluster/sdn/vnets", requestParams{query: runningQuery}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSDNVnet calls GET /cluster/sdn/vnets/{vnet}.
func (c *Client) GetSDNVnet(ctx context.Context, vnet string) (*SDNVnet, error) {
	var out SDNVnet
	path := fmt.Sprintf("/cluster/sdn/vnets/%s", vnet)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSDNSubnets calls GET /cluster/sdn/vnets/{vnet}/subnets.
func (c *Client) ListSDNSubnets(ctx context.Context, vnet string) ([]SDNSubnet, error) {
	var out []SDNSubnet
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets", vnet)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSDNSubnetsRunning calls GET /cluster/sdn/vnets/{vnet}/subnets?running=1:
// see ListSDNZonesRunning's doc comment.
func (c *Client) ListSDNSubnetsRunning(ctx context.Context, vnet string) ([]SDNSubnet, error) {
	var out []SDNSubnet
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets", vnet)
	if err := c.do(ctx, "GET", path, requestParams{query: runningQuery}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSDNSubnet calls GET /cluster/sdn/vnets/{vnet}/subnets/{subnet}.
func (c *Client) GetSDNSubnet(ctx context.Context, vnet, subnet string) (*SDNSubnet, error) {
	var out SDNSubnet
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets/%s", vnet, subnet)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSDNStatus calls GET /cluster/sdn: the full zone/vnet/subnet tree
// flattened with pending markers.
func (c *Client) GetSDNStatus(ctx context.Context) ([]SDNStatusEntry, error) {
	var out []SDNStatusEntry
	if err := c.do(ctx, "GET", "/cluster/sdn", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApplySDN calls PUT /cluster/sdn: apply all pending SDN changes cluster-wide
// via an async task (docs/features/sdn.md §4), returning the task's UPID.
// Poll it with GetTask or WaitTask. This is the "(4) sdn.apply last" step the
// change engine's planner appends when a changeset carries SDN ops
// (docs/data-model.md §3, docs/architecture.md §4).
func (c *Client) ApplySDN(ctx context.Context) (string, error) {
	var upid string
	if err := c.do(ctx, "PUT", "/cluster/sdn", requestParams{}, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// --- SDN writes (T-402: sdn.zone/vnet/subnet.* ops' "(1) cluster-scope PVE
// API calls" planner step, docs/data-model.md §3) -------------------------
//
// Every write stages a pending edit only — real PVE (and internal/pvemock,
// see that package's sdn.go) does not realize a zone/vnet/subnet create/
// update/delete until a subsequent ApplySDN (PUT /cluster/sdn) runs. The
// zone/vnet fields these calls need are exactly SDNZone/SDNVnet/SDNSubnet's
// own (defined above for the read paths); reusing them here rather than
// declaring separate *Write request types keeps one field list per entity
// instead of two that could drift.

// CreateSDNZone calls POST /cluster/sdn/zones. z.ID is sent as the "zone"
// field (SDNZone's own json tag) per PVE's create-by-body convention.
func (c *Client) CreateSDNZone(ctx context.Context, z SDNZone) error {
	return c.do(ctx, "POST", "/cluster/sdn/zones", requestParams{body: z}, nil)
}

// UpdateSDNZone calls PUT /cluster/sdn/zones/{zone}.
func (c *Client) UpdateSDNZone(ctx context.Context, id string, z SDNZone) error {
	path := fmt.Sprintf("/cluster/sdn/zones/%s", id)
	return c.do(ctx, "PUT", path, requestParams{body: z}, nil)
}

// DeleteSDNZone calls DELETE /cluster/sdn/zones/{zone}.
func (c *Client) DeleteSDNZone(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/zones/%s", id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateSDNVnet calls POST /cluster/sdn/vnets.
func (c *Client) CreateSDNVnet(ctx context.Context, v SDNVnet) error {
	return c.do(ctx, "POST", "/cluster/sdn/vnets", requestParams{body: v}, nil)
}

// UpdateSDNVnet calls PUT /cluster/sdn/vnets/{vnet}.
func (c *Client) UpdateSDNVnet(ctx context.Context, id string, v SDNVnet) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s", id)
	return c.do(ctx, "PUT", path, requestParams{body: v}, nil)
}

// DeleteSDNVnet calls DELETE /cluster/sdn/vnets/{vnet}.
func (c *Client) DeleteSDNVnet(ctx context.Context, id string) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s", id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}

// CreateSDNSubnet calls POST /cluster/sdn/vnets/{vnet}/subnets. s.ID must be
// the subnet's CIDR (docs/data-model.md's SdnSubnet.ID doc comment).
func (c *Client) CreateSDNSubnet(ctx context.Context, vnet string, s SDNSubnet) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets", vnet)
	return c.do(ctx, "POST", path, requestParams{body: s}, nil)
}

// UpdateSDNSubnet calls PUT /cluster/sdn/vnets/{vnet}/subnets/{subnet}.
func (c *Client) UpdateSDNSubnet(ctx context.Context, vnet, id string, s SDNSubnet) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets/%s", vnet, id)
	return c.do(ctx, "PUT", path, requestParams{body: s}, nil)
}

// DeleteSDNSubnet calls DELETE /cluster/sdn/vnets/{vnet}/subnets/{subnet}.
func (c *Client) DeleteSDNSubnet(ctx context.Context, vnet, id string) error {
	path := fmt.Sprintf("/cluster/sdn/vnets/%s/subnets/%s", vnet, id)
	return c.do(ctx, "DELETE", path, requestParams{}, nil)
}
