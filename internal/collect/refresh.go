// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// RefreshNow forces an immediate, targeted poll+ingest cycle outside the
// regular ticker cadence — used after a future change-engine apply (T-205)
// to get fresh state fast rather than waiting up to a full poll interval.
//
// scope.Node == "" refreshes everything (the same steps a full PVE poll
// cycle runs, plus the host/LLDP pollers if the local node is already
// known); scope.Node set to a specific node name refreshes only that
// node's PVE-visible state (its declared network config, its guests, its
// own and its guests' firewall rulesets — but not cluster-wide state like
// SDN or the cluster-scope firewall ruleset, which a single node's change
// cannot affect) plus, if that node is this daemon's own local node, a
// host/LLDP poll too. scope.Kinds is not consulted: RefreshNow always
// refreshes everything relevant to the given node scope, on the
// assumption that the caller already knows which node changed but not
// necessarily which entity kinds on it did.
//
// The whole cycle (however many underlying PVE/host calls it takes) is
// reported as exactly one merged Delta batch — see diffSnapshots — and
// forwarded to Config.OnDelta exactly once, if it is non-empty.
//
// Delta-attribution caveat (same as runLoop's attempt, see loop.go): the
// before/after snapshots below bracket this call on a shared graph, so
// changes a concurrently-ticking poll loop commits inside the window are
// attributed to this batch too and may also be reported by that loop's own
// batch. "Exactly one batch" is therefore per-RefreshNow-call, not a
// global exactly-once delivery of each change; consumers treat batches as
// idempotent re-read hints, which makes the overlap harmless.
func (c *Collector) RefreshNow(ctx context.Context, scope inventory.Scope) (inventory.Delta, error) {
	before := c.graph.Snapshot()
	now := time.Now()

	var errs []error

	pveStart := time.Now()
	pveErr := c.refreshPVE(ctx, scope)
	c.recordResult("pve", now, pveErr)
	c.reportPoll("pve", "", time.Since(pveStart), pveErr)
	if pveErr != nil {
		errs = append(errs, pveErr)
	}

	local := c.getLocalNode()
	if local != "" && (scope.Node == "" || scope.Node == local) {
		// hostErr/lldpErr's own OnPoll reporting happens inside
		// hostPollOnce/lldpPollOnce's per-node call sites (host.go), same as
		// the regular RunHostLoop/RunLLDPLoop path — not duplicated here.
		hostErr := c.hostPollOnce(ctx)
		c.recordResult("host", now, hostErr)
		if hostErr != nil {
			errs = append(errs, hostErr)
		}

		lldpStart := time.Now()
		lldpErr := c.lldpPollOnce(ctx)
		c.recordResult("lldp", now, lldpErr)
		c.reportPoll("lldp", local, time.Since(lldpStart), lldpErr)
		if lldpErr != nil {
			errs = append(errs, lldpErr)
		}
	}

	after := c.graph.Snapshot()
	delta := diffSnapshots(before, after)
	c.emitDelta("refresh", delta)

	return delta, errors.Join(errs...)
}

// refreshPVE runs the PVE-side steps of a targeted refresh.
func (c *Collector) refreshPVE(ctx context.Context, scope inventory.Scope) error {
	if scope.Node == "" {
		return c.pvePollAll(ctx)
	}

	node := scope.Node
	if err := c.pollNodeNetwork(ctx, node); err != nil {
		return err
	}

	resources, err := c.pve.ClusterResources(ctx)
	if err != nil {
		return fmt.Errorf("collect: cluster resources: %w", err)
	}

	c.pollGuests(ctx, []string{node}, resources)
	c.pollFirewall(ctx, []string{node}, resources, false)
	return nil
}
