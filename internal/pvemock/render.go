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
		b.WriteString(autostartPrefixLine(i))
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
		case "bridge":
			if i.BridgePorts != "" {
				fmt.Fprintf(&b, "\tbridge-ports %s\n", i.BridgePorts)
			} else {
				b.WriteString("\tbridge-ports none\n")
			}
			if i.BridgeVlanAware {
				b.WriteString("\tbridge-vlan-aware yes\n")
			}
			b.WriteString("\tbridge-stp off\n\tbridge-fd 0\n")
		case "OVSBridge":
			// OVS bridges use the ovs_* stanza vocabulary, not the Linux
			// bridge-*/bridge-stp options above (T-407: rendering these as
			// a plain Linux bridge misclassified every fixture-declared
			// OVSBridge on the FromInterfaces/inventory read path).
			if i.BridgePorts != "" {
				fmt.Fprintf(&b, "\tovs_ports %s\n", i.BridgePorts)
			}
			b.WriteString("\tovs_type OVSBridge\n")
		case "bond":
			if i.Slaves != "" {
				fmt.Fprintf(&b, "\tbond-slaves %s\n", i.Slaves)
			}
			if i.BondMode != "" {
				fmt.Fprintf(&b, "\tbond-mode %s\n", i.BondMode)
			}
		case "OVSBond":
			// VlanRawDevice doubles as the OVS bridge this bond attaches to
			// (ovs_bridge) for OVSBond/OVSIntPort fixture entries — see
			// this file's doc comment and inventory.FromPVENetwork's
			// OVSIntPort case, which reuses the same field for the same
			// reason (no live PVE cluster to validate against a dedicated
			// wire field name; matches this codebase's existing
			// field-reuse convention rather than adding a new one).
			if i.Slaves != "" {
				fmt.Fprintf(&b, "\tovs_bonds %s\n", i.Slaves)
			}
			b.WriteString("\tovs_type OVSBond\n")
			if i.VlanRawDevice != "" {
				fmt.Fprintf(&b, "\tovs_bridge %s\n", i.VlanRawDevice)
			}
			if i.BondMode != "" {
				fmt.Fprintf(&b, "\tovs_options %s\n", ovsBondModeOptions(i.BondMode))
			}
		case "vlan":
			if i.VlanRawDevice != "" {
				fmt.Fprintf(&b, "\tvlan-raw-device %s\n", i.VlanRawDevice)
			}
		case "OVSIntPort":
			b.WriteString("\tovs_type OVSIntPort\n")
			if i.VlanRawDevice != "" {
				fmt.Fprintf(&b, "\tovs_bridge %s\n", i.VlanRawDevice)
			}
			if i.VlanID != 0 {
				fmt.Fprintf(&b, "\tovs_options tag=%d\n", i.VlanID)
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

// autostartPrefixLine renders the "auto <name>"/"allow-*" line preceding an
// iface stanza, or "" when Autostart is false. OVS stanzas use ifupdown2's
// "allow-ovs"/"allow-<bridge>" convention instead of "auto" (matching
// testdata/interfaces/04-ovs-bridge.interfaces, 05-ovs-bond.interfaces) —
// both encode "start this at boot", but ifupdown2's OVS glue specifically
// keys off allow-*.
func autostartPrefixLine(i NetIface) string {
	if !i.Autostart {
		return ""
	}
	switch i.Type {
	case "OVSBridge":
		return fmt.Sprintf("allow-ovs %s\n", i.Iface)
	case "OVSBond", "OVSIntPort":
		if i.VlanRawDevice != "" {
			return fmt.Sprintf("allow-%s %s\n", i.VlanRawDevice, i.Iface)
		}
		return fmt.Sprintf("auto %s\n", i.Iface)
	default:
		return fmt.Sprintf("auto %s\n", i.Iface)
	}
}

// ovsBondModeOptions renders the ovs_options value for an OVS bond's mode,
// mirroring internal/change/ifaces.ovsBondModeOptions exactly (duplicated
// rather than imported: pvemock is a leaf test-fixture package that must
// not depend on internal/change, and the mapping is small/stable — see
// testdata/interfaces/05-ovs-bond.interfaces, the shared source of truth
// both copies render towards).
func ovsBondModeOptions(mode string) string {
	switch mode {
	case "802.3ad", "lacp":
		return "bond_mode=balance-slb lacp=active other_config:lacp-time=fast"
	default:
		return "bond_mode=" + mode
	}
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
