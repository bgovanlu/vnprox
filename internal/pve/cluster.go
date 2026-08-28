// SPDX-License-Identifier: Apache-2.0

package pve

import "context"

// ClusterStatus calls GET /cluster/status: the cluster summary row plus
// one row per member node (name, IP, online/quorate/local flags).
func (c *Client) ClusterStatus(ctx context.Context) ([]ClusterStatusEntry, error) {
	var wire []clusterStatusWire
	if err := c.do(ctx, "GET", "/cluster/status", requestParams{}, &wire); err != nil {
		return nil, err
	}
	out := make([]ClusterStatusEntry, len(wire))
	for i, w := range wire {
		out[i] = w.toEntry()
	}
	return out, nil
}

// ClusterResources calls GET /cluster/resources: a flat list of nodes,
// qemu guests, and lxc containers cluster-wide.
func (c *Client) ClusterResources(ctx context.Context) ([]ClusterResource, error) {
	var out []ClusterResource
	if err := c.do(ctx, "GET", "/cluster/resources", requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
