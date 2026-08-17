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

	// T-3101: SDN Fabrics + the two read-only route-policy families the
	// same capture exposed. ListSDNFabricNodes is its own read (real PVE's
	// own separate /cluster/sdn/fabrics/node collection — per-node fabric
	// membership is never inferred from ListSDNFabrics' own response).
	ListSDNFabrics(ctx context.Context) ([]pve.SDNFabric, error)
	ListSDNFabricNodes(ctx context.Context) ([]pve.SDNFabricNode, error)
	ListSDNPrefixLists(ctx context.Context) ([]pve.SDNPrefixList, error)
	ListSDNRouteMaps(ctx context.Context) ([]pve.SDNRouteMap, error)

	// ListSDNControllers (T-3102) is Controller's own read — see Controller's
	// doc comment for why it has no per-controller status sub-read the way
	// Fabric's ListSDNFabricNodes does.
	ListSDNControllers(ctx context.Context) ([]pve.SDNController, error)
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
	Zones []Zone `json:"zones"`
	// Fabrics (T-3101) is a sibling top-level collection, not nested under
	// Zone — a fabric is cluster underlay routing config a zone may ride on
	// (a vxlan zone's `--fabric` field), not a zone's child the way a Vnet
	// is.
	Fabrics []Fabric `json:"fabrics"`
	// Controllers (T-3102) is a sibling top-level collection too, not
	// nested under Zone — a controller is infrastructure a zone may ride on
	// (a zone's `controller` field, now a *reference* by id rather than an
	// opaque string, KindSDNController's doc comment) the same "sibling,
	// not child" shape Fabrics already established, since a controller is
	// referenced BY a zone, not owned BY one — deleting a zone must never
	// delete the controller it named.
	Controllers []Controller `json:"controllers"`
	// PrefixLists/RouteMaps (T-3101) are read-only BGP route-policy
	// objects — display only, no diff/pending (they carry no staged-vs-
	// running distinction of their own to render, unlike Zone/Vnet/Subnet).
	PrefixLists []PrefixList `json:"prefixLists"`
	RouteMaps   []RouteMap   `json:"routeMaps"`
	GeneratedAt int64        `json:"generatedAt"`
}

// Controller is one SDN controller (T-3102), mirroring pve.SDNController's
// field set — see internal/pve/sdn_controller.go's package doc comment for
// what each type-conditional field means. Unlike Fabric, it carries no
// NodeStatus: the captured API has no per-controller status route (like
// Fabric) AND no separate per-node-membership collection the way
// ListSDNFabricNodes gives Fabric either — a controller's Nodes/Node fields
// are pure configuration, not a membership list this service independently
// observes. EVPN/BGP session health is reported separately and re-attached
// to a controller's id by internal/evpn.Service (T-3102 acceptance
// criterion 3), not inferred here.
type Controller struct {
	ID                      string   `json:"id"`
	Type                    string   `json:"type"`
	Pending                 string   `json:"pending,omitempty"`
	BgpMode                 string   `json:"bgpMode,omitempty"`
	Fabric                  string   `json:"fabric,omitempty"`
	IsisDomain              string   `json:"isisDomain,omitempty"`
	IsisNet                 string   `json:"isisNet,omitempty"`
	Loopback                string   `json:"loopback,omitempty"`
	Node                    string   `json:"node,omitempty"`
	PeerGroupName           string   `json:"peerGroupName,omitempty"`
	RouteMapIn              string   `json:"routeMapIn,omitempty"`
	RouteMapOut             string   `json:"routeMapOut,omitempty"`
	Nodes                   []string `json:"nodes,omitempty"`
	Peers                   []string `json:"peers,omitempty"`
	IsisIfaces              []string `json:"isisIfaces,omitempty"`
	ASN                     int      `json:"asn,omitempty"`
	EbgpMultihop            int      `json:"ebgpMultihop,omitempty"`
	Ebgp                    bool     `json:"ebgp,omitempty"`
	BgpMultipathAsPathRelax bool     `json:"bgpMultipathAsPathRelax,omitempty"`
}

// Fabric is one SDN fabric (T-3101), mirroring pve.SDNFabric's field set —
// see internal/pve/sdn_fabric.go's package doc comment for what each
// protocol-conditional field means. NodeStatus is built from
// ListSDNFabricNodes filtered by this fabric's ID, mirroring how Zone.
// NodeStatus is built from GetSDNZoneStatus (Tree's own doc comment on the
// pattern) — except a fabric has no per-node *health* read in the captured
// API (no /cluster/sdn/fabrics/fabric/{id}/status route exists the way a
// zone's does), so NodeStatus here reports configured membership only:
// every node returned by ListSDNFabricNodes for this fabric gets Status
// "ok" (this mock/PVE gives no other signal to report), and Detail carries
// the node's assigned underlay IP (from IPPrefix/IP6Prefix's allocation)
// when known.
type Fabric struct {
	ID                  string       `json:"id"`
	Protocol            string       `json:"protocol"`
	Pending             string       `json:"pending,omitempty"`
	IPPrefix            string       `json:"ipPrefix,omitempty"`
	IP6Prefix           string       `json:"ip6Prefix,omitempty"`
	RouteFilter         string       `json:"routeFilter,omitempty"`
	Area                string       `json:"area,omitempty"`
	Redistribute        []string     `json:"redistribute,omitempty"`
	NodeStatus          []NodeStatus `json:"nodeStatus"`
	CSNPInterval        int          `json:"csnpInterval,omitempty"`
	HelloInterval       int          `json:"helloInterval,omitempty"`
	PersistentKeepalive int          `json:"persistentKeepalive,omitempty"`
}

// PrefixList is one read-only BGP prefix-list object (T-3101). Field shape
// beyond ID is unconfirmed against hardware — see internal/pve/
// sdn_fabric.go's package doc comment.
type PrefixList struct {
	ID string `json:"id"`
}

// RouteMap is one read-only BGP route-map object (T-3101). See
// PrefixList's doc comment.
type RouteMap struct {
	ID string `json:"id"`
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

	fabrics, err := s.buildFabrics(ctx)
	if err != nil {
		return Tree{}, err
	}

	controllers, err := s.buildControllers(ctx)
	if err != nil {
		return Tree{}, err
	}

	prefixLists, err := s.pve.ListSDNPrefixLists(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing prefix-lists: %w", err)
	}
	pls := make([]PrefixList, 0, len(prefixLists))
	for _, pl := range prefixLists {
		pls = append(pls, PrefixList{ID: pl.ID})
	}
	sort.Slice(pls, func(i, j int) bool { return pls[i].ID < pls[j].ID })

	routeMaps, err := s.pve.ListSDNRouteMaps(ctx)
	if err != nil {
		return Tree{}, fmt.Errorf("sdn: listing route-maps: %w", err)
	}
	rms := make([]RouteMap, 0, len(routeMaps))
	for _, rm := range routeMaps {
		rms = append(rms, RouteMap{ID: rm.ID})
	}
	sort.Slice(rms, func(i, j int) bool { return rms[i].ID < rms[j].ID })

	return Tree{
		Zones: zones, Fabrics: fabrics, Controllers: controllers,
		PrefixLists: pls, RouteMaps: rms, GeneratedAt: s.now().Unix(),
	}, nil
}

// buildControllers fetches the controller list and maps it straight to
// Controller — unlike buildFabrics, there is no per-node membership
// sub-read to fold in (see Controller's own doc comment).
func (s *Service) buildControllers(ctx context.Context) ([]Controller, error) {
	staged, err := s.pve.ListSDNControllers(ctx)
	if err != nil {
		return nil, fmt.Errorf("sdn: listing controllers: %w", err)
	}
	out := make([]Controller, 0, len(staged))
	for _, c := range staged {
		out = append(out, Controller{
			ID: c.ID, Type: c.Type, Pending: string(c.Pending),
			BgpMode: c.BgpMode, Fabric: c.Fabric, IsisDomain: c.IsisDomain, IsisNet: c.IsisNet,
			Loopback: c.Loopback, Node: c.Node, PeerGroupName: c.PeerGroupName,
			RouteMapIn: c.RouteMapIn, RouteMapOut: c.RouteMapOut,
			Nodes: c.Nodes, Peers: c.Peers, IsisIfaces: c.IsisIfaces,
			ASN: c.ASN, EbgpMultihop: c.EbgpMultihop, Ebgp: c.Ebgp,
			BgpMultipathAsPathRelax: c.BgpMultipathAsPathRelax,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// buildFabrics fetches the fabric list plus the cluster-wide per-node
// membership read, grouping the latter by fabric id — the same "sibling
// per-node read, grouped by owning id" pattern Tree already uses for a
// zone's own status (GetSDNZoneStatus), except fabrics have no independent
// per-node health signal to poll (see Fabric's own doc comment).
func (s *Service) buildFabrics(ctx context.Context) ([]Fabric, error) {
	staged, err := s.pve.ListSDNFabrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("sdn: listing fabrics: %w", err)
	}
	nodes, err := s.pve.ListSDNFabricNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("sdn: listing fabric nodes: %w", err)
	}
	nodesByFabric := map[string][]pve.SDNFabricNode{}
	for _, n := range nodes {
		nodesByFabric[n.Fabric] = append(nodesByFabric[n.Fabric], n)
	}

	fabrics := make([]Fabric, 0, len(staged))
	for _, f := range staged {
		fab := Fabric{
			ID: f.ID, Protocol: f.Protocol, Pending: string(f.Pending),
			IPPrefix: f.IPPrefix, IP6Prefix: f.IP6Prefix,
			CSNPInterval: f.CSNPInterval, HelloInterval: f.HelloInterval, RouteFilter: f.RouteFilter,
			Area: f.Area, Redistribute: f.Redistribute, PersistentKeepalive: f.PersistentKeepalive,
		}
		for _, n := range nodesByFabric[f.ID] {
			detail := n.IP
			if detail == "" {
				detail = n.IP6
			}
			fab.NodeStatus = append(fab.NodeStatus, NodeStatus{Node: n.Node, Status: "ok", Detail: detail})
		}
		sort.Slice(fab.NodeStatus, func(i, j int) bool { return fab.NodeStatus[i].Node < fab.NodeStatus[j].Node })
		if fab.NodeStatus == nil {
			fab.NodeStatus = []NodeStatus{}
		}
		fabrics = append(fabrics, fab)
	}
	sort.Slice(fabrics, func(i, j int) bool { return fabrics[i].ID < fabrics[j].ID })
	return fabrics, nil
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
