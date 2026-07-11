// Package sdn builds docs/api.md's GET /sdn response: the zone -> vnet ->
// subnet tree with per-node apply/health status, plus the staged-vs-running
// pending diff docs/features/sdn.md §1 calls for ("vnprox surfaces
// staged-vs-running as a first-class diff instead of a mystery 'pending'
// flag").
//
// Unlike internal/topology (which renders the already-collected,
// poll-cached inventory graph), this package reads PVE directly and live,
// per request — the same "exact live state, not a poll-cached view"
// precedent T-208's raw interfaces editor set (docs/api.md: "GET
// /nodes/{node}/interfaces/raw ... the raw Monaco editor's 'open' call").
// A diff view is inherently comparison-sensitive: showing a staged delta
// against inventory's last poll (up to one PVE poll interval stale) risks
// rendering a diff that no longer matches what an apply would actually do.
// SDN configuration is cluster-scoped PVE data (replicated via pmxcfs), so
// the local node's own PVE API always has the complete, authoritative view
// regardless of which node's vnproxd the browser is talking to
// (docs/architecture.md §1: "Cluster-wide config (SDN, firewall, VM NICs)
// goes through the PVE API from whichever node the user hit") — no peer
// fan-out is needed here, unlike node-local data such as LLDP or stats.
package sdn

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// PVEReader is the subset of *pve.Client this package needs: the staged
// (default) and running ("?running=1") SDN trees, and per-zone realization
// status. A small seam (docs/architecture.md's "small interface, real type
// satisfies it" pattern already used throughout this codebase — e.g.
// internal/api's TopologyService/PeerServer) so this package's dependency
// on the concrete client stays reviewable and test-doubleable.
type PVEReader interface {
	ListSDNZones(ctx context.Context) ([]pve.SDNZone, error)
	ListSDNZonesRunning(ctx context.Context) ([]pve.SDNZone, error)
	GetSDNZoneStatus(ctx context.Context, zone string) ([]pve.SDNZoneStatus, error)
	ListSDNVnets(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNVnetsRunning(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNSubnets(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
	ListSDNSubnetsRunning(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
}

// Service builds the GET /sdn tree from a PVEReader.
type Service struct {
	pve PVEReader
	now func() time.Time
}

// NewService builds a Service backed by reader (in production, the
// daemon's own read-only *pve.Client — the same one internal/collect polls
// with).
func NewService(reader PVEReader) *Service {
	return &Service{pve: reader, now: time.Now}
}

// Tree is docs/api.md's GET /sdn response shape (added by T-401; not in
// the original contract — flagged and documented in docs/api.md in this
// same change per docs/development.md's definition-of-done #4).
type Tree struct {
	Zones       []Zone `json:"zones"`
	GeneratedAt int64  `json:"generatedAt"`
}

// Zone is one zone in the tree, with its VNets nested and its per-node
// realization status alongside its own staged-vs-running diff.
type Zone struct {
	Diff       *PendingDiff `json:"pendingDiff,omitempty"`
	Pending    string       `json:"pending,omitempty"`
	Bridge     string       `json:"bridge,omitempty"`
	Controller string       `json:"controller,omitempty"`
	ID         string       `json:"id"`
	Type       string       `json:"type"`
	Nodes      []string     `json:"nodes,omitempty"`
	ExitNodes  []string     `json:"exitNodes,omitempty"`
	Peers      []string     `json:"peers,omitempty"`
	NodeStatus []NodeStatus `json:"nodeStatus"`
	Vnets      []Vnet       `json:"vnets"`
	MTU        int          `json:"mtu,omitempty"`
	VrfVxlan   int          `json:"vrfVxlan,omitempty"`
}

// Vnet is one VNet nested under its zone.
type Vnet struct {
	Diff      *PendingDiff `json:"pendingDiff,omitempty"`
	ID        string       `json:"id"`
	Zone      string       `json:"zone"`
	Alias     string       `json:"alias,omitempty"`
	Pending   string       `json:"pending,omitempty"`
	Subnets   []Subnet     `json:"subnets"`
	Tag       int          `json:"tag,omitempty"`
	VlanAware bool         `json:"vlanAware,omitempty"`
}

// Subnet is one subnet nested under its VNet. ID is the CIDR, matching
// docs/data-model.md's SdnSubnet.ID doc comment.
type Subnet struct {
	Diff           *PendingDiff `json:"pendingDiff,omitempty"`
	ID             string       `json:"id"`
	Vnet           string       `json:"vnet"`
	CIDR           string       `json:"cidr"`
	Gateway        string       `json:"gateway,omitempty"`
	DHCPRangeStart string       `json:"dhcpRangeStart,omitempty"`
	DHCPRangeEnd   string       `json:"dhcpRangeEnd,omitempty"`
	Pending        string       `json:"pending,omitempty"`
	SNAT           bool         `json:"snat,omitempty"`
}

// NodeStatus is one node's realization status for a zone (GET
// /cluster/sdn/zones/{zone}/status, docs/features/sdn.md §1: "every level
// shows per-node realization status (applied / pending / error)").
type NodeStatus struct {
	Node   string `json:"node"`
	Status string `json:"status"` // ok|pending|error
	Detail string `json:"detail,omitempty"`
}

// PendingDiff renders docs/features/sdn.md §1's "staged-vs-running as a
// first-class diff" for one zone/vnet/subnet. State is "new"|"changed"|
// "deleted" — an in-sync (Pending == "") object has no Diff at all (the
// field is omitted entirely; there is nothing to show). ChangedFields lists
// which top-level JSON fields differ between Staged and Running (only
// populated for "changed", where both are known); Staged/Running carry the
// two full objects (as plain JSON-shaped maps, id/pending stripped) for the
// UI's rendered diff view.
type PendingDiff struct {
	Running       map[string]any `json:"running,omitempty"`
	Staged        map[string]any `json:"staged,omitempty"`
	State         string         `json:"state"`
	ChangedFields []string       `json:"changedFields,omitempty"`
}

// Tree fetches the current staged and running SDN trees from PVE and
// assembles docs/api.md's GET /sdn response. Per-zone status lookups that
// fail are logged nowhere (this package has no logger seam) but simply
// leave that zone's NodeStatus empty rather than failing the whole
// request — one zone's status endpoint erroring shouldn't blank the entire
// cockpit.
func (s *Service) Tree(ctx context.Context) (Tree, error) {
	staged, err := s.pve.ListSDNZones(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing zones: %w", err)
	}
	running, err := s.pve.ListSDNZonesRunning(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing running zones: %w", err)
	}
	runningZones := indexByJSON(running, func(z pve.SDNZone) string { return z.ID })

	stagedVnets, err := s.pve.ListSDNVnets(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing vnets: %w", err)
	}
	runningVnetsList, err := s.pve.ListSDNVnetsRunning(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing running vnets: %w", err)
	}
	runningVnets := indexByJSON(runningVnetsList, func(v pve.SDNVnet) string { return v.ID })

	vnetsByZone := map[string][]pve.SDNVnet{}
	for _, v := range stagedVnets {
		vnetsByZone[v.Zone] = append(vnetsByZone[v.Zone], v)
	}

	zones := make([]Zone, 0, len(staged))
	for _, z := range staged {
		zone := Zone{
			ID: z.ID, Type: z.Type, Bridge: z.Bridge, Controller: z.Controller,
			Nodes: z.Nodes, ExitNodes: z.ExitNodes, Peers: z.Peers,
			MTU: z.MTU, VrfVxlan: z.VrfVxlan, Pending: string(z.Pending),
		}
		runObj, ok := runningZones[z.ID]
		zone.Diff = buildDiff(string(z.Pending), z, runObj, ok)

		if st, statusErr := s.pve.GetSDNZoneStatus(ctx, z.ID); statusErr == nil {
			for _, e := range st {
				zone.NodeStatus = append(zone.NodeStatus, NodeStatus{Node: e.Node, Status: e.Status, Detail: e.Detail})
			}
		}
		sort.Slice(zone.NodeStatus, func(i, j int) bool { return zone.NodeStatus[i].Node < zone.NodeStatus[j].Node })
		if zone.NodeStatus == nil {
			zone.NodeStatus = []NodeStatus{}
		}

		vnets := append([]pve.SDNVnet(nil), vnetsByZone[z.ID]...)
		sort.Slice(vnets, func(i, j int) bool { return vnets[i].ID < vnets[j].ID })
		for _, v := range vnets {
			vnet := Vnet{ID: v.ID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware, Pending: string(v.Pending)}
			vRunObj, vOk := runningVnets[v.ID]
			vnet.Diff = buildDiff(string(v.Pending), v, vRunObj, vOk)

			stagedSubnets, subErr := s.pve.ListSDNSubnets(ctx, v.ID)
			if subErr != nil {
				stagedSubnets = nil
			}
			runningSubnetsList, subErr := s.pve.ListSDNSubnetsRunning(ctx, v.ID)
			if subErr != nil {
				runningSubnetsList = nil
			}
			runningSubnets := indexByJSON(runningSubnetsList, func(sub pve.SDNSubnet) string { return sub.ID })

			sort.Slice(stagedSubnets, func(i, j int) bool { return stagedSubnets[i].ID < stagedSubnets[j].ID })
			for _, sub := range stagedSubnets {
				sn := Subnet{
					ID: sub.ID, Vnet: sub.Vnet, CIDR: sub.CIDR, Gateway: sub.Gateway,
					DHCPRangeStart: sub.DHCPRangeStart, DHCPRangeEnd: sub.DHCPRangeEnd,
					SNAT: sub.SNAT, Pending: string(sub.Pending),
				}
				sRunObj, sOk := runningSubnets[sub.ID]
				sn.Diff = buildDiff(string(sub.Pending), sub, sRunObj, sOk)
				vnet.Subnets = append(vnet.Subnets, sn)
			}
			if vnet.Subnets == nil {
				vnet.Subnets = []Subnet{}
			}
			zone.Vnets = append(zone.Vnets, vnet)
		}
		if zone.Vnets == nil {
			zone.Vnets = []Vnet{}
		}
		zones = append(zones, zone)
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].ID < zones[j].ID })

	return Tree{Zones: zones, GeneratedAt: s.now().Unix()}, nil
}

// indexByJSON indexes items by keyOf into a map, for matching a staged
// object against its running counterpart by id.
func indexByJSON[T any](items []T, keyOf func(T) string) map[string]T {
	out := make(map[string]T, len(items))
	for _, item := range items {
		out[keyOf(item)] = item
	}
	return out
}

// buildDiff derives one entity's PendingDiff from its own Pending marker
// (the authoritative source of "what kind of staged edit is this",
// mirroring PVE's own semantics) plus the matching running object, when
// one exists. Returns nil for an in-sync ("") object — nothing to show.
func buildDiff(pending string, staged, running any, runningOK bool) *PendingDiff {
	switch pending {
	case "new":
		return &PendingDiff{State: "new", Staged: toFieldMap(staged)}
	case "deleted":
		return &PendingDiff{State: "deleted", Staged: toFieldMap(staged)}
	case "changed":
		d := &PendingDiff{State: "changed", Staged: toFieldMap(staged)}
		if runningOK {
			d.Running = toFieldMap(running)
			d.ChangedFields = diffFields(d.Staged, d.Running)
		}
		return d
	default:
		return nil
	}
}

// toFieldMap marshals v (a pve.SDN{Zone,Vnet,Subnet}) to its JSON-shaped
// map form, stripping the "pending" marker itself — the diff is about
// content fields, and the marker is already surfaced as PendingDiff.State.
func toFieldMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	delete(m, "pending")
	return m
}

// diffFields returns the sorted list of top-level keys whose value differs
// (or whose presence differs) between staged and running.
func diffFields(staged, running map[string]any) []string {
	keys := map[string]bool{}
	for k := range staged {
		keys[k] = true
	}
	for k := range running {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		sv, sok := staged[k]
		rv, rok := running[k]
		if sok != rok || !reflect.DeepEqual(sv, rv) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
