package collect

import (
	"context"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// hostPollOnce polls the local host's netlink-equivalent link state
// (physical NICs, bonds, bridges, VLAN sub-interfaces) into
// SourceHostNetlink partials, and its interfaces(5) file into
// SourceHostInterfaces partials (inventory.FromInterfaces — the declared
// config that outranks pve-network for every declared field in the merge
// table), for whichever node the PVE poller has discovered as "local"
// (host.Reader's Real implementation only ever serves its own node — see
// that package's doc comment). Before that discovery has happened (process
// just started, or the PVE poller hasn't completed a cycle yet), this is a
// no-op, not an error, so the host loop does not spuriously back off
// before it has anything to poll.
//
// The poll's success/failure state is judged solely on the netlink Links()
// read: an interfaces-file read or parse failure is logged and skipped,
// leaving the previous SourceHostInterfaces contributions untouched rather
// than clobbering declared state the graph already has correct (the same
// keep-last-known policy pvePollAll applies to individual step failures).
//
// Interface counters (host.Reader.Stats) are read per deliverable 2 but
// still discarded: raw counters have no inventory entity fields at all —
// modeling them is internal/metrics' future job.
func (c *Collector) hostPollOnce(ctx context.Context) error {
	node := c.getLocalNode()
	if node == "" {
		return nil
	}

	links, err := c.host.Links(ctx, node)
	if err != nil {
		return fmt.Errorf("host links (%s): %w", node, err)
	}
	entities := inventory.FromNetlinkLinks(node, links)
	c.graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, entities)

	if _, statsErr := c.host.Stats(ctx, node); statsErr != nil {
		c.log.Debug("collect: host stats read failed", "node", node, "error", statsErr)
	}

	raw, err := c.host.InterfacesFile(ctx, node, false)
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
