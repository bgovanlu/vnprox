package ceph

import (
	"context"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// Discover reads PVE's own Ceph public/cluster network declaration
// (GET /cluster/ceph/config) and every named node's OSD placement
// (GET /nodes/{node}/ceph/osd), fanning the per-node call out sequentially
// across nodes (caller-supplied — this package has no opinion on cluster
// membership; cmd/vnproxd passes the same node-name list its
// pve.Client.ClusterStatus call already resolves). Every call is a plain
// GET through the existing *pve.Client — no new Ceph API client, no new
// credentials (see this package's doc comment). A node reported by nodes
// but running no OSDs (or no Ceph at all) simply contributes zero rows —
// never an error.
func Discover(ctx context.Context, client *pve.Client, nodes []string) (Status, error) {
	if client == nil {
		return Status{}, fmt.Errorf("ceph: discover: nil pve client")
	}

	cfg, err := client.CephConfig(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("ceph: discovering public/cluster network config: %w", err)
	}

	var osds []OSD
	for _, node := range nodes {
		list, err := client.CephOSDs(ctx, node)
		if err != nil {
			return Status{}, fmt.Errorf("ceph: discovering OSD placement on node %s: %w", node, err)
		}
		for _, o := range list {
			osds = append(osds, OSD{Ref: OSDRef(o.Node, o.ID), Device: o.Device, Node: o.Node, ID: o.ID, Up: o.Up, In: o.In})
		}
	}
	sort.Slice(osds, func(i, j int) bool {
		if osds[i].Node != osds[j].Node {
			return osds[i].Node < osds[j].Node
		}
		return osds[i].ID < osds[j].ID
	})

	return Status{PublicNetwork: cfg.PublicNetwork, ClusterNetwork: cfg.ClusterNetwork, OSDs: osds}, nil
}
