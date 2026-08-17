package collect

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// pvePollAll runs one full PVE poll cycle: cluster status (which also
// discovers every member node and this daemon's own "local" node),
// per-node network, guests, SDN, and firewall (cluster + every node +
// every guest). It is the pollFunc RunPVELoop drives.
//
// Only cluster status and cluster resources are treated as fatal to the
// whole cycle (returned as an error, which drives RunPVELoop's
// backoff/staleness): they are the two calls every other step in this
// cycle depends on. Failure of any other individual step (one node's
// network, one guest's config, one zone's status, one ruleset) is logged
// and skipped, leaving that entity's previously-known state untouched
// rather than clobbering it — so a transient single-node/single-guest
// hiccup does not blank out data the graph already has correct.
func (c *Collector) pvePollAll(ctx context.Context) error {
	nodes, err := c.pollClusterStatus(ctx)
	if err != nil {
		return err
	}
	c.retireDepartedNodes(nodes)

	for _, n := range nodes {
		if netErr := c.pollNodeNetwork(ctx, n); netErr != nil {
			c.log.Warn("collect: polling node network failed, skipping", "node", n, "error", netErr)
		}
	}

	resources, err := c.pve.ClusterResources(ctx)
	if err != nil {
		return fmt.Errorf("collect: cluster resources: %w", err)
	}

	c.pollGuests(ctx, nodes, resources)
	if err := c.pollSDN(ctx); err != nil {
		c.log.Warn("collect: polling SDN failed", "error", err)
	}
	c.pollFirewall(ctx, nodes, resources, true)
	return nil
}

// pollClusterStatus polls GET /cluster/status, reconciles one Node entity
// per member (each in its own node-scoped ApplyPoll call — Scope only
// supports a single Node value or the empty/cluster-scoped one, not "all
// these nodes at once"), records the "local" node for the host/LLDP
// pollers, and returns the member node name list every other PVE poll step
// in this cycle needs.
//
// Note this alone does NOT retire a node that left the cluster: a departed
// node is simply absent from entries, so no scoped ApplyPoll covering it is
// issued here (or by any other per-node poll step, which all iterate the
// current membership). pvePollAll handles retirement explicitly via
// retireDepartedNodes.
func (c *Collector) pollClusterStatus(ctx context.Context) ([]string, error) {
	entries, err := c.pve.ClusterStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect: cluster status: %w", err)
	}

	entities := inventory.FromClusterStatus(entries)
	byNode := make(map[string]inventory.Entity, len(entities))
	for _, e := range entities {
		byNode[e.GetRef().Node] = e
	}

	var nodes []string
	peers := make(map[string]peer.Peer, len(entries))
	for _, e := range entries {
		if e.Type != "node" {
			continue
		}
		nodes = append(nodes, e.Name)
		if e.Local {
			c.setLocalNode(e.Name)
		} else if c.peerClient != nil && e.IP != "" {
			// T-303: build the peer address book from the same
			// GET /cluster/status rows Peers() itself would filter to
			// (Type=="node", !Local, IP!="") — see peer.Client.Peers — so
			// the host loop's per-tick peer fan-out never pays for a
			// second PVE round trip just to rediscover addresses this
			// poll already fetched.
			peers[e.Name] = peer.Peer{Node: e.Name, Addr: net.JoinHostPort(e.IP, strconv.Itoa(c.peerClient.Port()))}
		}
		var ents []inventory.Entity
		if ent, ok := byNode[e.Name]; ok {
			ents = []inventory.Entity{ent}
		}
		c.graph.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{Node: e.Name}, ents)
	}
	if c.peerClient != nil {
		c.setPeers(peers)
	}
	if c.hostServesCluster {
		c.setClusterNodes(nodes)
	}
	return nodes, nil
}

// retireDepartedNodes compares the current cluster membership against the
// previous cluster-status poll's and, for every node that disappeared,
// issues one empty node-scoped ApplyPoll per PVE source — retiring the
// departed node's Node entity, pve-network interfaces, guests, and firewall
// rulesets, which would otherwise linger as stale ghosts forever (no
// per-node poll step covers a node the membership list no longer names).
//
// Host-side sources for this daemon's own local node are never retired
// here: local hardware does not vanish just because the local node's own
// cluster-status row briefly disappears, and the host loop keeps
// reconciling it regardless. Since T-303, a *departed peer's* host-side
// contributions (netlink links, the interfaces file — populated only when
// Config.Peer was fanning out to it) ARE retired below, the same as its
// PVE-sourced entities: once a node leaves the cluster, this daemon has no
// further way to reach it (peer discovery no longer lists it), so its
// last-known host state would otherwise linger as a stale ghost forever.
//
// The membership set is guarded by c.mu, and each retirement ApplyPoll is
// serialized by the graph itself, so concurrent cycles (the PVE loop plus a
// RefreshNow) are safe: at worst both observe the same departure and the
// second set of empty ApplyPolls is a no-op.
func (c *Collector) retireDepartedNodes(current []string) {
	cur := make(map[string]bool, len(current))
	for _, n := range current {
		cur[n] = true
	}

	c.mu.Lock()
	var departed []string
	for n := range c.seenNodes {
		if !cur[n] {
			departed = append(departed, n)
		}
	}
	c.seenNodes = cur
	localNode := c.localNode
	c.mu.Unlock()

	sort.Strings(departed)
	for _, n := range departed {
		c.log.Info("collect: node left the cluster, retiring its entities", "node", n)
		c.graph.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{Node: n}, nil)
		c.graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: n}, nil)
		c.graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: n}, nil)
		c.graph.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Node: n, Kinds: []inventory.Kind{inventory.KindFwRuleset}}, nil)
		if c.peerClient != nil && n != localNode {
			c.graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: n}, nil)
			c.graph.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{Node: n}, nil)
			c.retireHostNodeStatus(n)
		}
	}
}

// pollNodeNetwork polls one node's declared network config (GET
// /nodes/{node}/network) into SourcePVENetwork partials.
func (c *Collector) pollNodeNetwork(ctx context.Context, node string) error {
	ifaces, err := c.pve.ListNodeNetwork(ctx, node)
	if err != nil {
		return fmt.Errorf("node network (%s): %w", node, err)
	}
	entities := inventory.FromPVENetwork(node, ifaces)
	c.graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, entities)
	return nil
}

// pollGuests fetches every qemu/lxc guest config on targetNodes (resources
// is the already-fetched GET /cluster/resources listing, filtered here to
// targetNodes) and reconciles Guest/GuestNic partials one node-scoped
// ApplyPoll call at a time — including nodes with zero guests, so a
// deleted guest is retired rather than left stale.
func (c *Collector) pollGuests(ctx context.Context, targetNodes []string, resources []pve.ClusterResource) {
	nodeSet := make(map[string]bool, len(targetNodes))
	for _, n := range targetNodes {
		nodeSet[n] = true
	}

	var filtered []pve.ClusterResource
	configs := map[int]map[string]string{}
	for _, r := range resources {
		kind, ok := guestKind(r.Type)
		if !ok || !nodeSet[r.Node] {
			continue
		}
		filtered = append(filtered, r)
		cfg, err := c.pve.GetGuestConfig(ctx, r.Node, kind, r.VMID)
		if err != nil {
			c.log.Warn("collect: fetching guest config failed, skipping", "node", r.Node, "vmid", r.VMID, "error", err)
			continue
		}
		configs[r.VMID] = cfg
	}

	entities := inventory.FromPVEGuests(filtered, configs)
	byNode := make(map[string][]inventory.Entity, len(nodeSet))
	for _, e := range entities {
		byNode[e.GetRef().Node] = append(byNode[e.GetRef().Node], e)
	}
	for n := range nodeSet {
		c.graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: n}, byNode[n])
	}
}

// guestKind maps a GET /cluster/resources row's Type to a pve.GuestKind,
// reporting ok=false for non-guest rows (nodes, storage, ...).
func guestKind(resourceType string) (pve.GuestKind, bool) {
	switch resourceType {
	case string(pve.GuestQemu):
		return pve.GuestQemu, true
	case string(pve.GuestLXC):
		return pve.GuestLXC, true
	default:
		return "", false
	}
}

// pollSDN polls the cluster-wide SDN tree (zones, vnets, subnets, per-zone
// status) into cluster-scoped (empty Node) SdnZone/SdnVnet/SdnSubnet
// partials. Zone list and vnet list are treated as fatal to this step (the
// two calls everything else here depends on); a single zone's status or a
// single vnet's subnets failing is logged and skipped.
func (c *Collector) pollSDN(ctx context.Context) error {
	zones, err := c.pve.ListSDNZones(ctx)
	if err != nil {
		return fmt.Errorf("sdn zones: %w", err)
	}
	vnets, err := c.pve.ListSDNVnets(ctx)
	if err != nil {
		return fmt.Errorf("sdn vnets: %w", err)
	}

	subnets := make(map[string][]pve.SDNSubnet, len(vnets))
	for _, v := range vnets {
		subs, subErr := c.pve.ListSDNSubnets(ctx, v.ID)
		if subErr != nil {
			c.log.Warn("collect: listing SDN subnets failed, skipping", "vnet", v.ID, "error", subErr)
			continue
		}
		subnets[v.ID] = subs
	}

	zoneStatus := make(map[string][]pve.SDNZoneStatus, len(zones))
	for _, z := range zones {
		st, statusErr := c.pve.GetSDNZoneStatus(ctx, z.ID)
		if statusErr != nil {
			c.log.Warn("collect: getting SDN zone status failed, skipping", "zone", z.ID, "error", statusErr)
			continue
		}
		zoneStatus[z.ID] = st
	}

	entities := inventory.FromPVESDN(zones, vnets, subnets, zoneStatus)

	// T-1204: fold the SDN DNS plugin config (zones + their PowerDNS records)
	// into the same SourcePVESDN poll. A cluster with no DNS plugin
	// configured simply contributes nothing (an empty zone list is not an
	// error), and one zone's record read failing skips only that zone rather
	// than failing the whole SDN poll — the same tolerate-per-item posture
	// the subnet/zone-status reads above use.
	dnsZones, err := c.pve.ListSDNDnsZones(ctx)
	if err != nil {
		c.log.Warn("collect: listing SDN DNS zones failed, skipping DNS", "error", err)
	} else {
		dnsRecords := make(map[string][]pve.SDNDnsRecord, len(dnsZones))
		for _, z := range dnsZones {
			recs, recErr := c.pve.ListSDNDnsRecords(ctx, z.ID)
			if recErr != nil {
				c.log.Warn("collect: listing SDN DNS records failed, skipping", "zone", z.ID, "error", recErr)
				continue
			}
			dnsRecords[z.ID] = recs
		}
		entities = append(entities, inventory.FromPVEDNS(dnsZones, dnsRecords)...)
	}

	// T-3102: SDN controllers, folded into the same SourcePVESDN poll (a
	// controller IS live-polled here, unlike a fabric — see
	// inventory.KindSDNController's doc comment). A cluster with no
	// controllers configured contributes nothing.
	controllers, err := c.pve.ListSDNControllers(ctx)
	if err != nil {
		c.log.Warn("collect: listing SDN controllers failed, skipping", "error", err)
	} else {
		entities = append(entities, inventory.FromPVESDNControllers(controllers)...)
	}

	c.graph.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, entities)
	return nil
}

// pollFirewall polls firewall rulesets at cluster scope (if
// includeCluster), and at node + per-guest scope for every node in
// targetNodes (resources is the already-fetched cluster resources listing,
// used to enumerate each node's guests). All of one node's entities (its
// own ruleset plus every guest's) are reconciled in a single
// Scope{Node: n, Kinds: [fw-ruleset]}-scoped ApplyPoll call — the Kinds
// restriction mirrors ingest.go's own documented guidance ("a firewall
// poll that only enumerates fw-rulesets should not retire guests").
// Individual scope fetch failures are logged and skipped.
func (c *Collector) pollFirewall(ctx context.Context, targetNodes []string, resources []pve.ClusterResource, includeCluster bool) {
	fwKinds := []inventory.Kind{inventory.KindFwRuleset}

	if includeCluster {
		var clusterEnts []inventory.Entity
		if opts, rules, err := c.fetchFirewall(ctx, pve.ClusterFirewallScope()); err != nil {
			c.log.Warn("collect: fetching cluster firewall failed, skipping", "error", err)
		} else {
			ref := inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}
			objs := c.fetchFirewallObjects(ctx, pve.ClusterFirewallScope(), true)
			clusterEnts = inventory.FromPVEFirewall(ref, inventory.FwScopeCluster, opts, rules, objs)
		}
		// T-3103: vnet-scope firewall rulesets are cluster-wide like the
		// cluster ruleset itself (not per-node), so they are polled and
		// reconciled in this same includeCluster block/ApplyPoll call —
		// folding them into a *second* Scope{Kinds: fwKinds} call would
		// retire whichever set was applied first (both calls share the same
		// unscoped Scope, so the second overwrites rather than merges).
		if vnets, err := c.pve.ListSDNVnets(ctx); err != nil {
			c.log.Warn("collect: listing SDN vnets for firewall poll failed, skipping", "error", err)
		} else {
			for _, v := range vnets {
				opts, rules, ferr := c.fetchFirewall(ctx, pve.VnetFirewallScope(v.ID))
				if ferr != nil {
					c.log.Warn("collect: fetching vnet firewall failed, skipping", "vnet", v.ID, "error", ferr)
					continue
				}
				ref := inventory.Ref{Kind: inventory.KindFwRuleset, ID: "vnet/" + v.Zone + "/" + v.ID}
				// No fetchFirewallObjects call: hardware-captured, vnet
				// scope has no aliases/ipset endpoint (see FwScopeVNet's
				// doc comment) — mirrors node scope's own omission above.
				clusterEnts = append(clusterEnts, inventory.FromPVEFirewall(ref, inventory.FwScopeVNet, opts, rules, inventory.FirewallObjects{})...)
			}
		}
		c.graph.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: fwKinds}, clusterEnts)
	}

	for _, n := range targetNodes {
		var ents []inventory.Entity
		if opts, rules, err := c.fetchFirewall(ctx, pve.NodeFirewallScope(n)); err != nil {
			c.log.Warn("collect: fetching node firewall failed, skipping", "node", n, "error", err)
		} else {
			ref := inventory.Ref{Kind: inventory.KindFwRuleset, Node: n, ID: "node"}
			// No fetchFirewallObjects call here: hardware validation (T-608)
			// found real PVE has no node-scoped aliases/ipset endpoint at
			// all (404/"no handler" — unlike cluster and guest scope, which
			// both support it), matching its actual firewall model — a
			// node's own host firewall can only reference cluster-defined
			// aliases/ipsets, never define its own. pvemock previously
			// served this route anyway, masking the gap.
			ents = append(ents, inventory.FromPVEFirewall(ref, inventory.FwScopeNode, opts, rules, inventory.FirewallObjects{})...)
		}

		for _, r := range resources {
			if r.Node != n {
				continue
			}
			kind, ok := guestKind(r.Type)
			if !ok {
				continue
			}
			opts, rules, err := c.fetchFirewall(ctx, pve.GuestFirewallScope(n, kind, r.VMID))
			if err != nil {
				c.log.Warn("collect: fetching guest firewall failed, skipping", "node", n, "vmid", r.VMID, "error", err)
				continue
			}
			ref := inventory.Ref{Kind: inventory.KindFwRuleset, Node: n, ID: fmt.Sprintf("guest/%s/%d", kind, r.VMID)}
			objs := c.fetchFirewallObjects(ctx, pve.GuestFirewallScope(n, kind, r.VMID), false)
			ents = append(ents, inventory.FromPVEFirewall(ref, inventory.FwScopeGuest, opts, rules, objs)...)
		}

		c.graph.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Node: n, Kinds: fwKinds}, ents)
	}
}

// fetchFirewall fetches one scope's ruleset-level options and ordered rule
// list.
func (c *Collector) fetchFirewall(ctx context.Context, scope pve.FirewallScope) (pve.FirewallOptions, []pve.FirewallRule, error) {
	opts, err := c.pve.GetFirewallOptions(ctx, scope)
	if err != nil {
		return pve.FirewallOptions{}, nil, fmt.Errorf("firewall options: %w", err)
	}
	rules, err := c.pve.ListFirewallRules(ctx, scope)
	if err != nil {
		return pve.FirewallOptions{}, nil, fmt.Errorf("firewall rules: %w", err)
	}
	return *opts, rules, nil
}

// fetchFirewallObjects fetches one scope's aliases and ipsets (with
// entries), and — when includeGroups (cluster scope only; security groups
// are a cluster-only concept in real PVE, see inventory.FwGroup's doc
// comment) — the cluster-wide security groups with their rules. Individual
// object fetch failures (one ipset's entries, one group's rules) are
// logged and skipped, the same "don't blank out what's already known"
// policy every other step in this poll cycle follows; a failure to list
// aliases/ipsets/groups themselves just yields an empty result for that
// object kind this poll.
func (c *Collector) fetchFirewallObjects(ctx context.Context, scope pve.FirewallScope, includeGroups bool) inventory.FirewallObjects {
	var objs inventory.FirewallObjects

	if aliases, err := c.pve.ListFirewallAliases(ctx, scope); err != nil {
		c.log.Warn("collect: listing firewall aliases failed, skipping", "error", err)
	} else {
		objs.Aliases = aliases
	}

	sets, err := c.pve.ListFirewallIPSets(ctx, scope)
	if err != nil {
		c.log.Warn("collect: listing firewall ipsets failed, skipping", "error", err)
	} else {
		objs.IPSets = sets
		objs.IPSetEntries = make(map[string][]pve.FirewallIPSetEntry, len(sets))
		for _, s := range sets {
			entries, entriesErr := c.pve.ListFirewallIPSetEntries(ctx, scope, s.Name)
			if entriesErr != nil {
				c.log.Warn("collect: listing firewall ipset entries failed, skipping", "ipset", s.Name, "error", entriesErr)
				continue
			}
			objs.IPSetEntries[s.Name] = entries
		}
	}

	if !includeGroups {
		return objs
	}
	groups, err := c.pve.ListFirewallGroups(ctx)
	if err != nil {
		c.log.Warn("collect: listing firewall groups failed, skipping", "error", err)
		return objs
	}
	objs.Groups = groups
	objs.GroupRules = make(map[string][]pve.FirewallRule, len(groups))
	for _, g := range groups {
		rules, err := c.pve.GetFirewallGroupRules(ctx, g.Name)
		if err != nil {
			c.log.Warn("collect: getting firewall group rules failed, skipping", "group", g.Name, "error", err)
			continue
		}
		objs.GroupRules[g.Name] = rules
	}
	return objs
}
