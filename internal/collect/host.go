package collect

import (
	"context"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// nodeHostReader is the read surface hostPollStateFor needs, satisfied
// directly by host.Reader for the local node (Real's own doc comment: "only
// ever serves its own node") and by peerHostReader (below) for every other
// cluster node (T-303: "peer-API-backed reader for every other node" —
// docs/architecture.md §1's "peer vnproxd instances on other cluster nodes
// for node-local data"). Both concrete types are called identically by
// hostPollOnce, so ingestion is uniform regardless of transport.
type nodeHostReader interface {
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)
	Links(ctx context.Context, node string) ([]host.LinkState, error)
	Stats(ctx context.Context, node string) (map[string]host.IfaceStats, error)
	Services(ctx context.Context, node string) (map[string]bool, error)
}

// peerHostReader adapts a *peer.Client + a specific Peer address into a
// nodeHostReader, so hostPollStateFor can poll a remote cluster node exactly
// like it polls the local one.
type peerHostReader struct {
	client *peer.Client
	peer   peer.Peer
}

func (r peerHostReader) InterfacesFile(ctx context.Context, node string, includePending bool) (string, error) {
	return r.client.Interfaces(ctx, r.peer, node, includePending)
}

func (r peerHostReader) Links(ctx context.Context, node string) ([]host.LinkState, error) {
	return r.client.Links(ctx, r.peer, node)
}

func (r peerHostReader) Stats(ctx context.Context, node string) (map[string]host.IfaceStats, error) {
	return r.client.Stats(ctx, r.peer, node)
}

func (r peerHostReader) Services(ctx context.Context, node string) (map[string]bool, error) {
	return r.client.Services(ctx, r.peer, node)
}

// hostPollStateFor polls one node's netlink-equivalent link state (physical
// NICs, bonds, bridges, VLAN sub-interfaces) into SourceHostNetlink
// partials, and its interfaces(5) file into SourceHostInterfaces partials
// (inventory.FromInterfaces — the declared config that outranks
// pve-network for every declared field in the merge table), via reader
// (either the local host.Reader or a peerHostReader for a remote node).
//
// The poll's success/failure state is judged solely on the netlink Links()
// read: an interfaces-file read or parse failure is logged and skipped,
// leaving the previous SourceHostInterfaces contributions untouched rather
// than clobbering declared state the graph already has correct (the same
// keep-last-known policy pvePollAll applies to individual step failures).
//
// Interface counters (reader.Stats) have no inventory entity fields at all,
// so they never feed ApplyPoll — instead they are handed to Config.OnStats
// (T-601's internal/metrics.Sampler.Ingest, when configured) alongside this
// same links read, since the sampler needs Links for interface kind/speed/
// bond-slave metadata to make sense of the raw counters. A stats read
// failure does not affect this poll's returned error/success state.
func (c *Collector) hostPollStateFor(ctx context.Context, node string, reader nodeHostReader) error {
	links, err := reader.Links(ctx, node)
	if err != nil {
		return fmt.Errorf("host links (%s): %w", node, err)
	}
	entities := inventory.FromNetlinkLinks(node, links)
	c.graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, entities)

	if stats, statsErr := reader.Stats(ctx, node); statsErr != nil {
		c.log.Debug("collect: host stats read failed", "node", node, "error", statsErr)
	} else if c.onStats != nil {
		c.onStats(ctx, node, time.Now(), links, stats)
	}

	if c.onServices != nil {
		if services, svcErr := reader.Services(ctx, node); svcErr != nil {
			c.log.Debug("collect: host service-status read failed", "node", node, "error", svcErr)
		} else {
			c.onServices(node, services)
		}
	}

	raw, err := reader.InterfacesFile(ctx, node, false)
	if err != nil {
		c.log.Debug("collect: host interfaces file read failed, keeping previous declared state", "node", node, "error", err)
		return nil
	}
	parsed, err := host.ParseInterfaces([]byte(raw))
	if err != nil {
		c.log.Warn("collect: parsing host interfaces file failed, keeping previous declared state", "node", node, "error", err)
		return nil
	}
	c.graph.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{Node: node}, inventory.FromInterfaces(node, parsed))
	return nil
}

// hostPollOnce polls this daemon's own local node (via c.host) and, when
// Config.Peer is set, every other currently-known cluster member (via a
// peerHostReader built from the address book pollClusterStatus most
// recently discovered) — T-303's "local host.Reader for self, peer-API-
// backed reader for every other node, uniform ingestion into inventory"
// deliverable.
//
// Before the PVE poller has discovered the local node (process just
// started, or it hasn't completed a cycle yet), this is a no-op, not an
// error, so the host loop does not spuriously back off before it has
// anything to poll — matching pre-T-303 behavior exactly for the local
// node.
//
// This poll step's returned error (and therefore RunHostLoop/RefreshNow's
// backoff/staleness accounting for the "host" name) reflects only the local
// node's result, exactly as before T-303: a peer being unreachable must not
// throttle this daemon's own local polling cadence. Each polled node
// (local and every peer) additionally gets its own per-node staleness
// record via recordNodeResult, which is what makes a single dead peer's
// band go stale (docs/features/topology.md §5) without affecting any other
// node's.
func (c *Collector) hostPollOnce(ctx context.Context) error {
	localNode := c.getLocalNode()
	if localNode == "" {
		return nil
	}

	start := time.Now()
	localErr := c.hostPollStateFor(ctx, localNode, c.host)
	c.recordNodeResult(localNode, start, localErr)
	c.reportPoll("host", localNode, time.Since(start), localErr)

	// T-2801: one cluster-wide fixture reader answers for every node, so
	// every other node is polled through the SAME reader as the local one
	// and no peer address is ever dialled. Mutually exclusive with the peer
	// fan-out below by construction: a demo daemon is built with no peer
	// client at all.
	if c.hostServesCluster {
		for _, node := range c.getClusterNodes() {
			if node == localNode {
				continue
			}
			nStart := time.Now()
			err := c.hostPollStateFor(ctx, node, c.host)
			c.recordNodeResult(node, nStart, err)
			c.reportPoll("host", node, time.Since(nStart), err)
			if err != nil {
				c.log.Warn("collect: cluster-wide host reader failed for a node, keeping last-known state", "node", node, "error", err)
			}
		}
	}

	if c.peerClient != nil {
		for _, p := range c.getPeers() {
			if p.Node == localNode {
				continue
			}
			pStart := time.Now()
			reader := peerHostReader{client: c.peerClient, peer: p}
			err := c.hostPollStateFor(ctx, p.Node, reader)
			c.recordNodeResult(p.Node, pStart, err)
			c.reportPoll("host", p.Node, time.Since(pStart), err)
			if err != nil {
				c.log.Warn("collect: peer host poll failed, keeping last-known state", "node", p.Node, "peer_addr", p.Addr, "error", err)
			}
		}
	}

	return localErr
}

// lldpPollOnce polls LLDP neighbor data for the local node into
// SourceHostLLDP partials. Like hostPollOnce, it is a no-op until the PVE
// poller has discovered the local node.
//
// T-302: a bare inventory.FromLLDP result cannot be handed to ApplyPoll
// as-is — ApplyPoll's normal per-Source scope reconciliation removes any
// previously-contributed Ref this poll's entity list omits, which would
// make a neighbor vanish from the graph the instant a single poll misses it
// (a transient lldpctl hiccup, or the neighbor's own LLDP TTL lapsing on the
// wire) instead of greying per docs/features/lldp-discovery.md §3's
// TTL/10-minute staleness lifecycle. inventory.RetainStaleLLDP folds
// forward any not-yet-dropped previously-seen neighbors this poll didn't
// refresh; see its doc comment.
func (c *Collector) lldpPollOnce(ctx context.Context) error {
	node := c.getLocalNode()
	if node == "" {
		return nil
	}

	raw, err := c.host.LLDP(ctx, node)
	if err != nil {
		return fmt.Errorf("lldp (%s): %w", node, err)
	}
	now := time.Now()
	fresh, err := inventory.FromLLDP(node, raw, now)
	if err != nil {
		return fmt.Errorf("parsing lldp (%s): %w", node, err)
	}
	entities := inventory.RetainStaleLLDP(c.graph.Snapshot(), node, fresh, now)
	c.graph.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: node}, entities)
	return nil
}
