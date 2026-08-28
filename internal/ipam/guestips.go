// SPDX-License-Identifier: Apache-2.0

// guestips.go implements T-1305's guest->IP resolution seam:
// Service.GuestIPs, the dependency GET /conntrack's `guest=` filter resolves
// through (docs/architecture.md's InventorySource is guest-IP-blank by
// design — see internal/sim's own GuestIPs doc comment — so this package's
// enrichment observations, the one real source of guest-claimed addresses
// this codebase has, is exactly where that resolution belongs).

package ipam

import (
	"context"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// GuestIPs resolves guestRef (an inventory Guest ref, e.g.
// "guest:pve1:104") to every IP address vnprox currently associates with
// it: the guest's own live guest-agent-reported addresses (qemu only, via
// agentObservations — the same source this package's subnet merge already
// reads) plus any PVE-IPAM allocation whose MAC matches one of the guest's
// known NICs. This is a direct collection of those two enrichment sources
// for one guest, not a subnet-scoped Cell merge (Allocations) — the
// conntrack explorer only needs the guest's IP set to filter live
// connections by, not IPAM's state/confidence rendering of it.
//
// Like every enrichment observation this package surfaces, a resolved
// address is "observed, never authoritative" (docs/features/ipam.md §1's
// confidence-labeling convention) — GuestIPs makes no claim these
// addresses are the guest's *only* addresses, only that they are the ones
// vnprox currently has evidence for. An unknown/never-observed guestRef
// simply resolves to no addresses, not an error.
func (s *Service) GuestIPs(ctx context.Context, guestRef string) ([]string, error) {
	snap := s.inv.Snapshot()
	seen := map[string]bool{}
	var out []string
	add := func(ip string) {
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		out = append(out, ip)
	}

	for _, o := range s.agentObservations(ctx, snap) {
		if o.GuestRef == guestRef {
			add(o.IP)
		}
	}

	macs := map[string]bool{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || nic.Guest.String() != guestRef || nic.Mac == "" {
			continue
		}
		macs[normMAC(nic.Mac)] = true
	}
	if len(macs) > 0 {
		allocByCIDR, err := s.allocationsByCIDR(ctx)
		if err == nil {
			for _, allocs := range allocByCIDR {
				for _, a := range allocs {
					if a.MAC != "" && macs[normMAC(a.MAC)] {
						add(a.IP)
					}
				}
			}
		}
	}

	sort.Strings(out)
	return out, nil
}
