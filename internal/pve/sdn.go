package pve

import (
	"context"
	"fmt"
)

// ListSDNZones calls GET /cluster/sdn/zones.
func (c *Client) ListSDNZones(ctx context.Context) ([]SDNZone, error) {
	var out []SDNZone
	if err := c.do(ctx, "GET", "/cluster/sdn/zones", requestParams{}, &out); err != nil {
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
