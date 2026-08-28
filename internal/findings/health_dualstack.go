// SPDX-License-Identifier: Apache-2.0

// health_dualstack.go implements T-1404's "dualstack_drift" health check:
// the classic silent dual-stack failure where an IPv4 path works and the
// identical IPv6 path is broken — never caught unless something actually
// compares the two verdicts side by side. This check does exactly that,
// reusing internal/sim (T-503) rather than re-implementing any routing
// logic: for every SDN VNet that carries both a v4 and a v6 subnet
// (dual-stack), it simulates that VNet's own path to external once per
// family (Src: the subnet's own gateway address — the natural
// representative address for "can this segment reach out at all",
// available without any guest IP resolution) and flags the case where v4
// simulates allow while v6 simulates deny/unreachable/indeterminate.
//
// Deliberately structural, not hysteresis-debounced (the same "nothing to
// debounce" reasoning health_orphanvnet.go's doc comment gives): whether a
// VNet's v4 and v6 subnets each have an external path is a property of the
// current SDN config (SNAT/exit-node/gateway), re-derived fresh every
// cycle from the same source of truth GET /sdn itself renders, not a noisy
// live counter.
//
// Scope note: a VNet whose subnet has no gateway configured is skipped
// (nothing to use as the representative source address) — this check
// never guesses an address. Firewall-rule-driven per-guest dual-stack
// drift (an IPv4-only ALLOW rule with no IPv6 counterpart) is a distinct,
// narrower case this VNet-boundary check does not attempt to catch — see
// this task's completion report for the scope note.

package findings

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sim"
)

const CheckDualstackDrift = "dualstack_drift"

const dualstackDriftDocsLink = "docs/features/ipam.md#3-workflow"

// dualstackPair is one VNet's own v4/v6 subnet pair.
type dualstackPair struct {
	v4, v6 *inventory.SdnSubnet
}

// checkDualstackDrift returns one finding per dual-stack-capable VNet whose
// v4 path to external simulates allow while its v6 path does not.
func checkDualstackDrift(snap inventory.Snapshot) []Finding {
	byVnet := map[string]*dualstackPair{}
	for _, e := range snap.All() {
		sub, ok := e.(*inventory.SdnSubnet)
		if !ok {
			continue
		}
		pfx, err := netip.ParsePrefix(sub.ID)
		if err != nil {
			continue
		}
		pair := byVnet[sub.Vnet]
		if pair == nil {
			pair = &dualstackPair{}
			byVnet[sub.Vnet] = pair
		}
		if pfx.Addr().Is4() {
			pair.v4 = sub
		} else {
			pair.v6 = sub
		}
	}

	vnetIDs := make([]string, 0, len(byVnet))
	for id := range byVnet {
		vnetIDs = append(vnetIDs, id)
	}
	sort.Strings(vnetIDs)

	engine := sim.NewEngine(sim.Input{Inventory: snap})
	var out []Finding
	for _, vnetID := range vnetIDs {
		pair := byVnet[vnetID]
		if pair.v4 == nil || pair.v6 == nil {
			continue // not dual-stack — nothing to compare
		}
		if pair.v4.Gateway == "" || pair.v6.Gateway == "" {
			continue // no representative source address, never guessed
		}

		v4Res := engine.Simulate(sim.Request{
			Src: sim.Endpoint{Kind: sim.EndpointIP, IP: pair.v4.Gateway},
			Dst: sim.Endpoint{Kind: sim.EndpointExternal}, Family: sim.FamilyV4,
		})
		v6Res := engine.Simulate(sim.Request{
			Src: sim.Endpoint{Kind: sim.EndpointIP, IP: pair.v6.Gateway},
			Dst: sim.Endpoint{Kind: sim.EndpointExternal}, Family: sim.FamilyV6,
		})

		if v4Res.Verdict != sim.VerdictAllow {
			continue
		}
		if v6Res.Verdict == sim.VerdictAllow {
			continue // healthy dual-stack — both families reach external
		}

		detail := fmt.Sprintf(
			"VNet %s: IPv4 (subnet %s) reaches external (%s) but IPv6 (subnet %s) does not (%s) — a dual-stack silent failure: the v4 path works while v6 is broken",
			vnetID, pair.v4.ID, v4Res.Verdict, pair.v6.ID, v6Res.Verdict)
		f := newHealthFinding(CheckDualstackDrift, SeverityWarning, detail, nil,
			[]string{pair.v4.GetRef().String(), pair.v6.GetRef().String()})
		f.DocsLink = dualstackDriftDocsLink
		out = append(out, f)
	}

	sortFindings(out)
	return out
}
