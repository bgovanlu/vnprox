package collect

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// hostPollOnce polls the local host's netlink-equivalent link state
// (physical NICs, bonds, bridges, VLAN sub-interfaces) into
// SourceHostNetlink partials for whichever node the PVE poller has
// discovered as "local" (host.Reader's Real implementation only ever
// serves its own node — see that package's doc comment). Before that
// discovery has happened (process just started, or the PVE poller hasn't
// completed a cycle yet), this is a no-op, not an error, so the host loop
// does not spuriously back off before it has anything to poll.
//
// It also reads the interfaces(5) file and interface counters, per
// deliverable 2's "interfaces file, netlink links, stats" — these are not
// yet fed into the inventory graph (there is no ingest.go adapter for
// either: SourceHostInterfaces is reserved in internal/inventory's merge
// ownership table for a future consumer, most likely T-204's change
// engine, which needs the lossless AST rather than resolved entities; raw
// interface counters have no inventory entity fields at all — that is
// internal/metrics' future job). Failures reading them are logged at
// Debug and do not affect this poll's success/failure state, which is
// judged solely on the netlink Links() read that inventory actually
// consumes.
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

	if _, err := c.host.Stats(ctx, node); err != nil {
		c.log.Debug("collect: host stats read failed", "node", node, "error", err)
	}
	if _, err := c.host.InterfacesFile(ctx, node, false); err != nil {
		c.log.Debug("collect: host interfaces file read failed", "node", node, "error", err)
	}
	return nil
}

// lldpPollOnce polls LLDP neighbor data for the local node into
// SourceHostLLDP partials. Like hostPollOnce, it is a no-op until the PVE
// poller has discovered the local node.
func (c *Collector) lldpPollOnce(ctx context.Context) error {
	node := c.getLocalNode()
	if node == "" {
		return nil
	}

	raw, err := c.host.LLDP(ctx, node)
	if err != nil {
		return fmt.Errorf("lldp (%s): %w", node, err)
	}
	entities, err := inventory.FromLLDP(node, raw)
	if err != nil {
		return fmt.Errorf("parsing lldp (%s): %w", node, err)
	}
	c.graph.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: node}, entities)
	return nil
}
