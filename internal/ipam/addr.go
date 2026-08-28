// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"math"
	"math/big"
	"net"
	"sort"
)

// maxFreeCount clamps a FreeRange.Count so a very large IPv6 gap (a /64 has
// 2^64 addresses, far beyond an int) still renders a sane, finite number.
// IPv4 subnets never approach this, so the clamp only ever affects large
// IPv6 prefixes, where an exact free-address count is meaningless anyway.
const maxFreeCount = math.MaxInt32

// ipToBig interprets ip as an unsigned big-endian integer of its own byte
// length (4 for IPv4, 16 for IPv6). Returns nil if ip is not a valid IP.
func ipToBig(ip net.IP) *big.Int {
	if v4 := ip.To4(); v4 != nil {
		return new(big.Int).SetBytes(v4)
	}
	if v16 := ip.To16(); v16 != nil {
		return new(big.Int).SetBytes(v16)
	}
	return nil
}

// bigToIP renders v as an IP of byteLen bytes (4 for IPv4, 16 for IPv6),
// left-padding with zeroes and truncating any overflow high bytes.
func bigToIP(v *big.Int, byteLen int) net.IP {
	b := v.Bytes()
	if len(b) > byteLen {
		b = b[len(b)-byteLen:]
	}
	out := make(net.IP, byteLen)
	copy(out[byteLen-len(b):], b)
	return out
}

// hostSpan returns the inclusive [lo, hi] integer bounds of cidr's usable
// host addresses and the address byte length. For an IPv4 subnet /30 or
// wider (>=4 addresses) the network and broadcast addresses are excluded
// (matching how a "usable range" reads in practice); a /31 or /32, or any
// IPv6 prefix, spans every address in the block. ok is false if cidr does
// not parse.
func hostSpan(cidr string) (lo, hi *big.Int, byteLen int, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, 0, false
	}
	ones, bits := ipnet.Mask.Size()
	byteLen = bits / 8
	base := ipToBig(ipnet.IP.Mask(ipnet.Mask))
	if base == nil {
		return nil, nil, 0, false
	}
	total := new(big.Int).Lsh(big.NewInt(1), uint(bits-ones))
	lo = new(big.Int).Set(base)
	hi = new(big.Int).Add(base, new(big.Int).Sub(total, big.NewInt(1)))
	if ipnet.IP.To4() != nil && total.Cmp(big.NewInt(4)) >= 0 {
		lo.Add(lo, big.NewInt(1)) // skip network address
		hi.Sub(hi, big.NewInt(1)) // skip broadcast address
	}
	return lo, hi, byteLen, true
}

// freeRanges computes the contiguous runs of unallocated host addresses in
// cidr, given the occupied set (any non-free Cell map, keyed by IP). It walks
// only the occupied addresses (sorted), emitting the gap before each and the
// trailing gap after the last — O(occupied log occupied), never proportional
// to the address space, which is what lets the address list scale to a /16.
func freeRanges(cidr string, occupied map[string]Cell) []FreeRange {
	lo, hi, byteLen, ok := hostSpan(cidr)
	if !ok {
		return nil
	}

	occ := make([]*big.Int, 0, len(occupied))
	for ipStr := range occupied {
		v := ipToBig(net.ParseIP(ipStr))
		if v == nil || v.Cmp(lo) < 0 || v.Cmp(hi) > 0 {
			continue
		}
		occ = append(occ, v)
	}
	sort.Slice(occ, func(i, j int) bool { return occ[i].Cmp(occ[j]) < 0 })

	one := big.NewInt(1)
	var out []FreeRange
	emit := func(start, end *big.Int) { // inclusive; no-op if start > end
		if start.Cmp(end) > 0 {
			return
		}
		count := new(big.Int).Add(new(big.Int).Sub(end, start), one)
		out = append(out, FreeRange{
			Start: bigToIP(start, byteLen).String(),
			End:   bigToIP(end, byteLen).String(),
			Count: clampCount(count),
		})
	}

	cursor := new(big.Int).Set(lo)
	for _, v := range occ {
		emit(cursor, new(big.Int).Sub(v, one))
		cursor = new(big.Int).Add(v, one)
	}
	emit(cursor, hi)
	return out
}

func clampCount(v *big.Int) int {
	if v.Cmp(big.NewInt(int64(maxFreeCount))) > 0 {
		return maxFreeCount
	}
	return int(v.Int64())
}
