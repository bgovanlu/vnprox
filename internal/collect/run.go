package collect

import "context"

// RunPVELoop drives the PVE poll loop on the configured PVE interval until
// ctx is cancelled. Its signature matches cmd/vnproxd's runGroup actor
// type, so wiring it in is `g.add(collector.RunPVELoop)`.
func (c *Collector) RunPVELoop(ctx context.Context) error {
	return c.runLoop(ctx, "pve", c.pveInterval, c.pvePollAll)
}

// RunHostLoop drives the local-host poll loop (netlink links, interfaces
// file, stats) on the configured host interval until ctx is cancelled.
// Wire in with `g.add(collector.RunHostLoop)`.
func (c *Collector) RunHostLoop(ctx context.Context) error {
	return c.runLoop(ctx, "host", c.hostInterval, c.hostPollOnce)
}

// RunLLDPLoop drives the LLDP poll loop on its own, longer configured
// interval until ctx is cancelled (docs/deployment.md's lldp_interval is
// separate from host_interval). Wire in with `g.add(collector.RunLLDPLoop)`.
func (c *Collector) RunLLDPLoop(ctx context.Context) error {
	return c.runLoop(ctx, "lldp", c.lldpInterval, c.lldpPollOnce)
}
