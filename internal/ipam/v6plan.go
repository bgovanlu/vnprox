// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	"github.com/bgovanlu/vnprox/internal/pve"
)

var (
	// ErrInvalidPrefix is returned by V6Plan for a malformed or
	// non-IPv6 (or over-specific) prefix argument.
	ErrInvalidPrefix = errors.New("ipam: invalid v6 prefix")
	// ErrPrefixTooLarge is returned by V6Plan when the requested prefix
	// would enumerate more than maxV6PlanBlocks /64 blocks.
	ErrPrefixTooLarge = errors.New("ipam: prefix too large to plan")
)

// v6PlanBlockLen is the planning grid's fixed block size: PVE SDN (and
// nearly every real-world v6 addressing plan) treats /64 as the atomic
// per-subnet unit — a shorter prefix delegated to the site (a /48 or /56
// from the upstream ISP/RIR, docs/features/ipam.md's "prefix delegation")
// is always sliced into /64s, never anything finer.
const v6PlanBlockLen = 64

// maxV6PlanBlocks caps how many /64 blocks V6Plan will ever enumerate —
// a defensive bound against an absurdly large delegated prefix (a /32,
// say) turning one request into a multi-million-row response; 65536
// covers every prefix length a real ISP/RIR delegation realistically uses
// (/48 and shorter is exceedingly rare for a home/small-site PD) with
// headroom to spare.
const maxV6PlanBlocks = 65536

// V6PlanBlock is one /64-aligned block of a delegated prefix.
type V6PlanBlock struct {
	CIDR  string `json:"cidr"`
	Vnet  string `json:"vnet,omitempty"`
	Zone  string `json:"zone,omitempty"`
	Alias string `json:"alias,omitempty"`
	// State is "allocated" (a real SdnSubnet already occupies this exact
	// /64 block), "proposed" (this plan assigns it to a v4-only VNet that
	// has no v6 subnet yet — Vnet/Zone/Alias name the target), or "free"
	// (neither — available for a future VNet or manual assignment).
	State string `json:"state"`
}

// V6PlanResponse is GET /ipam/subnets/{prefix}/v6-plan's response.
type V6PlanResponse struct {
	Prefix         string        `json:"prefix"`
	Blocks         []V6PlanBlock `json:"blocks"`
	GeneratedAt    int64         `json:"generatedAt"`
	PrefixLen      int           `json:"prefixLen"`
	BlockPrefixLen int           `json:"blockPrefixLen"`
	TotalBlocks    int           `json:"totalBlocks"`
}

// V6Plan builds docs/api.md's GET /ipam/subnets/{prefix}/v6-plan response:
// given a delegated IPv6 prefix (e.g. a /56), enumerates its /64-aligned
// blocks, marks the ones an already-configured SDN v6 subnet occupies as
// "allocated", and proposes the remaining free blocks (in ascending CIDR
// order) one-for-one to every currently v4-only VNet (sorted by VNet ID
// for determinism) that has no v6 subnet of its own yet — the "aligned to
// existing VLANs" half of the planning grid. DHCPv6-PD from an upstream
// device vnprox doesn't manage is visibility-only elsewhere (GET
// /ipv6/segments); this route never writes anything — see mountIPAMRoutes'
// regression test that no PD request is ever issued.
func (s *Service) V6Plan(ctx context.Context, prefix string) (V6PlanResponse, error) {
	pfx, err := netip.ParsePrefix(prefix)
	if err != nil || !pfx.Addr().Is6() {
		return V6PlanResponse{}, fmt.Errorf("ipam: v6plan: %w: %q is not a valid IPv6 prefix", ErrInvalidPrefix, prefix)
	}
	pfx = pfx.Masked()
	if pfx.Bits() > v6PlanBlockLen {
		return V6PlanResponse{}, fmt.Errorf("ipam: v6plan: %w: prefix /%d is more specific than /%d, nothing to plan",
			ErrInvalidPrefix, pfx.Bits(), v6PlanBlockLen)
	}
	blockCount := 1 << uint(v6PlanBlockLen-pfx.Bits())
	if blockCount > maxV6PlanBlocks {
		return V6PlanResponse{}, fmt.Errorf("ipam: v6plan: %w: prefix /%d would enumerate %d /64 blocks, over the %d-block cap",
			ErrPrefixTooLarge, pfx.Bits(), blockCount, maxV6PlanBlocks)
	}

	blocks := make([]V6PlanBlock, blockCount)
	blockByCIDR := make(map[string]int, blockCount)
	addr := pfx.Addr()
	for i := 0; i < blockCount; i++ {
		cidr := netip.PrefixFrom(addr, v6PlanBlockLen).String()
		blocks[i] = V6PlanBlock{CIDR: cidr, State: "free"}
		blockByCIDR[cidr] = i
		addr = nextBlock(addr, v6PlanBlockLen)
	}

	vnets, err := s.pve.ListSDNVnets(ctx)
	if err != nil {
		return V6PlanResponse{}, fmt.Errorf("ipam: v6plan: listing SDN vnets: %w", err)
	}
	vnetByID := make(map[string]pve.SDNVnet, len(vnets))
	for _, v := range vnets {
		vnetByID[v.ID] = v
	}

	subnetsBySubnet, err := s.sdnSubnets(ctx)
	if err != nil {
		return V6PlanResponse{}, fmt.Errorf("ipam: v6plan: %w", err)
	}
	hasV6 := map[string]bool{} // vnet ID -> already has a v6 subnet
	hasV4 := map[string]bool{} // vnet ID -> already has a v4 subnet
	for _, sub := range subnetsBySubnet {
		subPfx, err := netip.ParsePrefix(sub.cidr)
		if err != nil {
			continue
		}
		if subPfx.Addr().Is6() {
			hasV6[sub.vnet] = true
			if subPfx.Bits() == v6PlanBlockLen && pfx.Contains(subPfx.Masked().Addr()) {
				if i, ok := blockByCIDR[subPfx.Masked().String()]; ok {
					blocks[i].State = "allocated"
					blocks[i].Vnet = sub.vnet
					blocks[i].Zone = sub.zone
					if v, ok := vnetByID[sub.vnet]; ok {
						blocks[i].Alias = v.Alias
					}
				}
			}
		} else {
			hasV4[sub.vnet] = true
		}
	}

	// v4-only targets: a VNet with a v4 subnet already configured but no
	// v6 subnet of its own — sorted by ID for a deterministic, reviewable
	// proposal order.
	var targets []pve.SDNVnet
	for _, v := range vnets {
		if hasV4[v.ID] && !hasV6[v.ID] {
			targets = append(targets, v)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })

	ti := 0
	for i := range blocks {
		if ti >= len(targets) {
			break
		}
		if blocks[i].State != "free" {
			continue
		}
		blocks[i].State = "proposed"
		blocks[i].Vnet = targets[ti].ID
		blocks[i].Zone = targets[ti].Zone
		blocks[i].Alias = targets[ti].Alias
		ti++
	}

	now := s.now()
	return V6PlanResponse{
		Prefix: pfx.String(), PrefixLen: pfx.Bits(), BlockPrefixLen: v6PlanBlockLen,
		TotalBlocks: blockCount, Blocks: blocks, GeneratedAt: now.Unix(),
	}, nil
}

// nextBlock returns the first address of the /64 block immediately after
// the one starting at addr — addr must itself already be a /64 block
// boundary (V6Plan only ever calls this on values it computed that way).
// Implemented as 64-bit big-endian increment of the address's upper 8
// bytes (the /64 network portion), leaving the low 8 bytes (host portion,
// always zero for a block boundary) untouched.
func nextBlock(addr netip.Addr, blockPrefixLen int) netip.Addr {
	b := addr.As16()
	// blockPrefixLen is always 64 in this file (v6PlanBlockLen) — the
	// network portion is exactly the first 8 bytes.
	byteIdx := blockPrefixLen / 8
	for i := byteIdx - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			break
		}
	}
	return netip.AddrFrom16(b)
}
