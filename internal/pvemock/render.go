package pvemock

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RenderInterfaces renders a structured network config back into
// ifupdown2(5) /etc/network/interfaces stanza syntax — the literal text
// host.Reader.InterfacesFile returns and (were this a real host) that
// ifupdown2 would parse. PVE's own network API is structured, but the file
// on disk is not, so this bridges the two representations.
func RenderInterfaces(ifaces []NetIface) string {
	var b strings.Builder
	b.WriteString("# This file describes the network interfaces available on your system\n")
	b.WriteString("# and how to activate them. For more information, see interfaces(5).\n\n")
	b.WriteString("source /etc/network/interfaces.d/*\n\n")

	sorted := append([]NetIface(nil), ifaces...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Iface < sorted[j].Iface })

	for _, i := range sorted {
		if i.Autostart {
			fmt.Fprintf(&b, "auto %s\n", i.Iface)
		}
		method := i.Method
		if method == "" {
			method = "manual"
		}
		fmt.Fprintf(&b, "iface %s inet %s\n", i.Iface, method)
		if i.Address != "" {
			addr, mask, ok := splitCIDR(i.Address)
			if ok {
				fmt.Fprintf(&b, "\taddress %s\n\tnetmask %s\n", addr, mask)
			} else {
				fmt.Fprintf(&b, "\taddress %s\n", i.Address)
			}
		}
		if i.Gateway != "" {
			fmt.Fprintf(&b, "\tgateway %s\n", i.Gateway)
		}
		if i.MTU != 0 {
			fmt.Fprintf(&b, "\tmtu %d\n", i.MTU)
		}
		switch i.Type {
		case "bridge", "OVSBridge":
			if i.BridgePorts != "" {
				fmt.Fprintf(&b, "\tbridge-ports %s\n", i.BridgePorts)
			} else {
				b.WriteString("\tbridge-ports none\n")
			}
			if i.BridgeVlanAware {
				b.WriteString("\tbridge-vlan-aware yes\n")
			}
			b.WriteString("\tbridge-stp off\n\tbridge-fd 0\n")
		case "bond":
			if i.Slaves != "" {
				fmt.Fprintf(&b, "\tbond-slaves %s\n", i.Slaves)
			}
			if i.BondMode != "" {
				fmt.Fprintf(&b, "\tbond-mode %s\n", i.BondMode)
			}
		case "vlan":
			if i.VlanRawDevice != "" {
				fmt.Fprintf(&b, "\tvlan-raw-device %s\n", i.VlanRawDevice)
			}
		}
		if i.Comments != "" {
			for _, line := range strings.Split(i.Comments, "\n") {
				fmt.Fprintf(&b, "\t#%s\n", line)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func splitCIDR(cidr string) (addr, mask string, ok bool) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	prefix := 0
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return "", "", false
		}
		prefix = prefix*10 + int(c-'0')
	}
	return parts[0], prefixToNetmask(prefix), true
}

func prefixToNetmask(prefix int) string {
	if prefix < 0 {
		prefix = 0
	}
	if prefix > 32 {
		prefix = 32
	}
	var octets [4]byte
	for i := range 4 {
		bits := prefix - i*8
		switch {
		case bits >= 8:
			octets[i] = 0xFF
		case bits <= 0:
			octets[i] = 0x00
		default:
			octets[i] = byte(0xFF << (8 - bits))
		}
	}
	return fmt.Sprintf("%d.%d.%d.%d", octets[0], octets[1], octets[2], octets[3])
}

// lldpNeighborJSON is a small, faithful-enough approximation of
// `lldpctl -f json` output for the local ifaces named in the map keys.
type lldpNeighborJSON struct {
	Local string `json:"local-iface"`
	LLDPNeighbor
}

func marshalLLDP(neighbors map[string]LLDPNeighbor) ([]byte, error) {
	out := make([]lldpNeighborJSON, 0, len(neighbors))
	for iface, n := range neighbors {
		out = append(out, lldpNeighborJSON{Local: iface, LLDPNeighbor: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Local < out[j].Local })
	return json.Marshal(out)
}
