// suggest.go implements T-603 AC4's "address params get next-free
// suggestions" directly off inventory.Snapshot — see doc.go's note on why
// this does not go through internal/ipam/T-405's picker (neither exists
// on this branch's base).

package blueprint

import (
	"fmt"
	"net"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// usedAddresses collects every IPv4 address already declared on a bridge,
// vlan interface, or sdn subnet gateway anywhere in snap, keyed by its
// bare string form (no prefix) for membership checks.
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
func SuggestAddress(snap inventory.Snapshot, pool string) (string, error) {
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
// SuggestAddress's result for it. For a ParamIP param (a bare-IP field
// such as an SDN subnet's gateway, which carries no prefix of its own) the
// "/<prefixlen>" suffix is stripped before returning, since the pool's
// prefix length is only ever a search-scoping detail for that field, not
// part of its value.
func SuggestForParam(bp *Blueprint, paramName string, snap inventory.Snapshot) (string, error) {
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
		suggestion, err := SuggestAddress(snap, pool)
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
