package pve

import (
	"context"
	"fmt"
	"net/url"
)

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
