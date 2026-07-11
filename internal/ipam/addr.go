package ipam

import (
	"net"
	"strconv"
)

// hostAddresses returns every host address inside cidr, in ascending
// order, as dotted-quad/colon-hex strings: for a IPv4 subnet with a /30 or
// wider mask (>=4 addresses) the network and broadcast addresses are
// excluded (matching how the grid's "usable range" reads in practice); a
// /31 or /32 (or any IPv6 prefix) includes every address in the block,
// since there is no meaningful network/broadcast pair to exclude there.
// ok is false if cidr does not parse.
func hostAddresses(cidr string) (addrs []string, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, false
	}
	ones, bits := ipnet.Mask.Size()
	total := 1 << (bits - ones)
	isIPv4 := ipnet.IP.To4() != nil

	start := 0
	end := total
	if isIPv4 && total >= 4 {
		start = 1
		end = total - 1
	}

	base := ipnet.IP.Mask(ipnet.Mask)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, offsetIP(base, i).String())
	}
	return out, true
}

// offsetIP returns base + n, treating base as a big-endian integer of its
// own byte length (4 for IPv4, 16 for IPv6).
func offsetIP(base net.IP, n int) net.IP {
	ip := append(net.IP(nil), base...)
	for i := len(ip) - 1; i >= 0 && n > 0; i-- {
		sum := int(ip[i]) + n
		ip[i] = byte(sum & 0xff)
		n = sum >> 8
	}
	return ip
}

// blockCIDRs splits cidr into contiguous /24-sized (or, for IPv6, /120 —
// same 256-address block size) blocks for the paged large-subnet view
// (docs/features/ipam.md §2: "larger subnets render as paged block
// summaries"). Returns nil, false if cidr does not parse or is already
// <=256 addresses (the caller's direct-render threshold — no paging
// needed).
func blockCIDRs(cidr string) (blocks []string, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, false
	}
	ones, bits := ipnet.Mask.Size()
	blockOnes := bits - 8 // /24 for IPv4, /120 for IPv6
	if ones >= blockOnes {
		return nil, false
	}
	blockCount := 1 << (blockOnes - ones)
	base := ipnet.IP.Mask(ipnet.Mask)
	blockSize := 1 << 8
	out := make([]string, 0, blockCount)
	for i := 0; i < blockCount; i++ {
		blockBase := offsetIP(base, i*blockSize)
		out = append(out, blockBase.String()+cidrSuffix(blockOnes))
	}
	return out, true
}

func cidrSuffix(ones int) string {
	return "/" + strconv.Itoa(ones)
}
