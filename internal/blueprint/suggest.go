// SPDX-License-Identifier: Apache-2.0

// suggest.go implements T-603 AC4's "address params get next-free
// suggestions": SuggestAddress delegates to internal/ipam's own
// next-free-address computation (Service.Allocations — the same call
// web/src/ipam/NextFreePicker.tsx makes; see AddressPicker's doc comment)
// when an AddressPicker is wired, and falls back to a cruder,
// inventory.Snapshot-only heuristic otherwise. See doc.go for the history:
// this delegation was deferred since T-405 shipped internal/ipam and
// web/src/ipam/NextFreePicker.tsx, and is implemented here.

package blueprint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

// AddressPicker is the seam SuggestAddress delegates next-free-address
// selection to when IPAM is configured: internal/ipam.Service's own
// Allocations method (GET /ipam/subnets/{cidr}/allocations) — the exact
// call web/src/ipam/NextFreePicker.tsx makes, whose suggestion is
// literally `data.freeRanges[0].start` (docs/features/ipam.md §3).
// *ipam.Service satisfies this directly; no adapter is needed.
//
// Delegating buys real conflict avoidance the inventory-only fallback in
// this file cannot see: a PVE-IPAM reservation with no matching bridge/
// vlan/gateway address anywhere in inventory.Snapshot, an address a guest
// agent currently reports as in use but never reserved in IPAM, or an
// address IPAM has already flagged as a duplicate-IP/allocated-dark
// conflict — all of these are folded out of freeRanges by
// internal/ipam's merge (mergeSubnet, internal/ipam/merge.go), so a
// delegated suggestion can never land on one. The fallback heuristic only
// ever looks at addresses declared on a bridge, vlan interface, or SDN
// subnet gateway already in inventory.Snapshot, so it can and does
// suggest any of the three.
type AddressPicker interface {
	Allocations(ctx context.Context, cidr string) (ipam.AllocationList, error)
}

// usedAddresses collects every IPv4 address already declared on a bridge,
// vlan interface, or sdn subnet gateway anywhere in snap, keyed by its
// bare string form (no prefix) for membership checks. This is the
// fallback heuristic's data source — see AddressPicker's doc comment for
// what it misses relative to a delegated IPAM suggestion.
func usedAddresses(snap inventory.Snapshot) map[string]bool {
	used := map[string]bool{}
	add := func(cidrs []string) {
		for _, c := range cidrs {
			ip, _, err := net.ParseCIDR(c)
			if err != nil {
				if parsed := net.ParseIP(c); parsed != nil {
					ip = parsed
				} else {
					continue
				}
			}
			used[ip.String()] = true
		}
	}
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Bridge:
			add(v.Addresses)
		case *inventory.VlanIface:
			add(v.Addresses)
		case *inventory.SdnSubnet:
			if v.Gateway != "" {
				if ip := net.ParseIP(v.Gateway); ip != nil {
					used[ip.String()] = true
				}
			}
		}
	}
	return used
}

// SuggestAddress returns the first free IPv4 host address in pool (a
// CIDR, e.g. "192.168.10.0/24") not already declared anywhere in snap,
// formatted as "<ip>/<prefixlen>" matching pool's own prefix length. The
// network and broadcast addresses are always skipped.
//
// When picker is non-nil, SuggestAddress delegates to it first (see
// AddressPicker's doc comment for why that catches more than the
// inventory-only fallback below can). Delegation is bypassed — falling
// back to scanning snap via usedAddresses — in exactly two cases:
//
//   - picker is nil: no IPAM configured for this deployment at all. A
//     blueprint must still be usable without IPAM configured (the product
//     rule this fallback exists to satisfy), so this is not an error.
//   - picker returns ipam.ErrSubnetNotFound: pool names a subnet PVE
//     doesn't know about yet — the common case when a blueprint is about
//     to *create* a new subnet, which by definition has no IPAM
//     allocations (or even an SDN/bridge existence) to query.
//
// Any other picker error (e.g. PVE temporarily unreachable) also falls
// back rather than blocking the suggestion entirely — logged at warn
// level so a persistent IPAM outage stays visible — since a degraded
// suggestion is still strictly better than none for a form-fill helper
// that never itself applies anything (see this file's package doc).
//
// A picker that *does* resolve pool but reports no free range is
// deliberately not a fallback case: that is IPAM's authoritative answer
// that the subnet is full, and surfacing it as an error — rather than
// silently handing back an address the cruder heuristic merely doesn't
// happen to know is taken — is the whole point of delegating.
func SuggestAddress(ctx context.Context, picker AddressPicker, snap inventory.Snapshot, pool string) (string, error) {
	if picker != nil {
		addr, ok, err := suggestFromPicker(ctx, picker, pool)
		if err != nil {
			return "", err
		}
		if ok {
			return addr, nil
		}
	}
	return suggestFromSnapshot(snap, pool)
}

// suggestFromPicker delegates to picker for pool's next-free address. ok
// is false when the caller should fall back to the inventory-only
// heuristic instead (picker doesn't know pool yet, or a non-fatal picker
// error); err is non-nil only when IPAM authoritatively found no free
// address, which should surface directly rather than fall back.
func suggestFromPicker(ctx context.Context, picker AddressPicker, pool string) (addr string, ok bool, err error) {
	list, err := picker.Allocations(ctx, pool)
	if err != nil {
		if errors.Is(err, ipam.ErrSubnetNotFound) {
			return "", false, nil
		}
		slog.Warn("blueprint: IPAM address picker unavailable, falling back to inventory heuristic",
			"pool", pool, "error", err)
		return "", false, nil
	}
	if len(list.FreeRanges) == 0 {
		return "", false, fmt.Errorf("blueprint: pool %q has no free host addresses (per IPAM)", pool)
	}
	return fmt.Sprintf("%s/%d", list.FreeRanges[0].Start, list.Prefix), true, nil
}

// suggestFromSnapshot is SuggestAddress's pre-delegation heuristic,
// unchanged since before AddressPicker existed: the first free IPv4 host
// address in pool per usedAddresses(snap) alone.
func suggestFromSnapshot(snap inventory.Snapshot, pool string) (string, error) {
	_, ipnet, err := net.ParseCIDR(pool)
	if err != nil {
		return "", fmt.Errorf("blueprint: suggesting address in %q: %w", pool, err)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("blueprint: suggesting address in %q: only IPv4 pools are supported", pool)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("blueprint: suggesting address in %q: not an IPv4 network", pool)
	}
	used := usedAddresses(snap)

	hostBits := 32 - ones
	total := 1 << uint(hostBits)
	if total <= 2 {
		return "", fmt.Errorf("blueprint: pool %q has no usable host addresses", pool)
	}
	for i := 1; i < total-1; i++ { // skip network (offset 0) and broadcast (last offset)
		ip := addOffset(base, i)
		if !used[ip.String()] {
			return fmt.Sprintf("%s/%d", ip.String(), ones), nil
		}
	}
	return "", fmt.Errorf("blueprint: pool %q has no free host addresses", pool)
}

func addOffset(base net.IP, offset int) net.IP {
	v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	v += uint32(offset)
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// SuggestForParam resolves the address pool for one of bp's
// AddressSuggest-eligible params (its own Subnet field, or — if unset —
// the containing network of its Default CIDR) and returns
// SuggestAddress's result for it (delegating to picker when non-nil — see
// AddressPicker's doc comment). For a ParamIP param (a bare-IP field such
// as an SDN subnet's gateway, which carries no prefix of its own) the
// "/<prefixlen>" suffix is stripped before returning, since the pool's
// prefix length is only ever a search-scoping detail for that field, not
// part of its value.
func SuggestForParam(ctx context.Context, picker AddressPicker, bp *Blueprint, paramName string, snap inventory.Snapshot) (string, error) {
	for _, p := range bp.Params {
		if p.Name != paramName {
			continue
		}
		if !p.AddressSuggest {
			return "", fmt.Errorf("%w: param %q is not address-suggest-eligible", ErrInvalidParams, paramName)
		}
		pool := p.Subnet
		if pool == "" {
			def, ok := p.Default.(string)
			if !ok {
				return "", fmt.Errorf("%w: param %q has no subnet and no CIDR/IP default to infer one from", ErrInvalidParams, paramName)
			}
			cidr := def
			if p.Type == ParamIP {
				// A bare-IP default has no prefix of its own; Subnet is
				// required in that case unless the default happens to
				// already look like a CIDR (defensive, not expected).
				return "", fmt.Errorf("%w: param %q is type ip and must set subnet explicitly (no prefix to infer one from)", ErrInvalidParams, paramName)
			}
			_, ipnet, err := net.ParseCIDR(cidr)
			if err != nil {
				return "", fmt.Errorf("%w: param %q default %q is not a CIDR: %v", ErrInvalidParams, paramName, def, err)
			}
			pool = ipnet.String()
		}
		suggestion, err := SuggestAddress(ctx, picker, snap, pool)
		if err != nil {
			return "", err
		}
		if p.Type == ParamIP {
			if ip, _, ok := strings.Cut(suggestion, "/"); ok {
				return ip, nil
			}
		}
		return suggestion, nil
	}
	return "", fmt.Errorf("%w: no such param %q", ErrNotFound, paramName)
}
