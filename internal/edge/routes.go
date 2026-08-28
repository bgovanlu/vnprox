// SPDX-License-Identifier: Apache-2.0

package edge

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// NodeInterfaces is one node's raw /etc/network/interfaces content — the
// input ProjectRoutes/ProjectNAT parse, mirroring exactly what
// ChangesetService.ReadRawInterfaces already returns per node (docs/api.md's
// "GET /nodes/{node}/interfaces/raw").
type NodeInterfaces struct {
	Node    string
	Content string
}

// DefaultRoute is one node's default gateway, as declared on some iface
// stanza's own "gateway" option — the same field iface.update's Gateway
// param writes (docs/data-model.md §3: "a node's *default* gateway stays
// owned by the existing iface.update's gateway field"). ProjectRoutes never
// invents a second way to represent it.
type DefaultRoute struct {
	Node    string `json:"node"`
	Iface   string `json:"iface"`
	Gateway string `json:"gateway"`
}

// StaticRoute is one route.static.* rule, decoded straight from its
// generated post-up marker line — see internal/host.StaticRouteConfig.
type StaticRoute struct {
	ID       string `json:"id"`
	Node     string `json:"node"`
	Iface    string `json:"iface"`
	DestCIDR string `json:"destCidr"`
	Gateway  string `json:"gateway"`
	Comment  string `json:"comment,omitempty"`
	Metric   int    `json:"metric,omitempty"`
}

// RoutesView is GET /edge/routes' response shape.
type RoutesView struct {
	DefaultRoutes []DefaultRoute `json:"defaultRoutes"`
	StaticRoutes  []StaticRoute  `json:"staticRoutes"`
	GeneratedAt   int64          `json:"generatedAt"`
}

// ProjectRoutes parses every node's interfaces file and projects
// RoutesView: one DefaultRoute per iface stanza carrying a non-empty
// "gateway" option, plus one StaticRoute per route.static.* rule found via
// its generated marker (see internal/host.DecodeStaticRouteMarker).
// Deterministic ordering (input node order, then file order within a node)
// so a golden test's expected output is stable.
func ProjectRoutes(nodes []NodeInterfaces) (RoutesView, error) {
	var out RoutesView
	for _, n := range nodes {
		f, err := host.ParseInterfaces([]byte(n.Content))
		if err != nil {
			return RoutesView{}, fmt.Errorf("edge: parsing interfaces for node %s: %w", n.Node, err)
		}
		for _, e := range f.Ifaces() {
			if gw, ok := e.Get("gateway"); ok && gw != "" {
				out.DefaultRoutes = append(out.DefaultRoutes, DefaultRoute{Node: n.Node, Iface: e.Name, Gateway: gw})
			}
			for _, item := range e.Body {
				if item.Kind != host.BodyOption || item.Key != "post-up" {
					continue
				}
				s, ok := host.CutEdgeMarker(item.Value)
				if !ok {
					continue
				}
				c, ok := host.DecodeStaticRouteMarker(s)
				if !ok {
					continue
				}
				out.StaticRoutes = append(out.StaticRoutes, StaticRoute{
					ID: c.ID, Node: n.Node, Iface: c.Iface, DestCIDR: c.DestCIDR,
					Gateway: c.Gateway, Metric: c.Metric, Comment: c.Comment,
				})
			}
		}
	}
	return out, nil
}
