package docexport

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Section headings are exported as constants: the golden test (build_test.go)
// asserts each is present verbatim, and html.go reuses the exact same
// strings for its own headings so Markdown and HTML never name a section
// differently.
const (
	HeadingTopology    = "Topology overview"
	HeadingInterfaces  = "Per-node interfaces"
	HeadingVlanMatrix  = "VLAN matrix"
	HeadingSDN         = "SDN inventory"
	HeadingFirewall    = "Firewall summary"
	HeadingLLDP        = "LLDP wiring"
	docTitle           = "vnprox network documentation"
	noneObservedMarker = "_none observed_"
)

// Markdown renders d as a standalone Markdown document (docs/features/
// blueprints.md §4). It carries no image data (the topology SVG is an
// HTML-only embed — see html.go's doc comment); the topology section here
// is a text summary instead.
func Markdown(d Data) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", docTitle)
	fmt.Fprintf(&b, "_Generated: %s (%d)_\n\n", time.Unix(d.GeneratedAt, 0).UTC().Format(time.RFC3339), d.GeneratedAt)

	writeTopologySummaryMD(&b, d)
	writeInterfacesMD(&b, d)
	writeVlanMatrixMD(&b, d)
	writeSDNMD(&b, d)
	writeFirewallMD(&b, d)
	writeLLDPMD(&b, d)

	return b.String()
}

func writeTopologySummaryMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingTopology)
	fmt.Fprintf(b, "- Cluster nodes: %d\n", len(d.Nodes))
	fmt.Fprintf(b, "- Topology elements: %d nodes, %d edges\n", len(d.Topology.Nodes), len(d.Topology.Edges))
	layers := make([]string, len(d.Topology.Layers))
	for i, l := range d.Topology.Layers {
		layers[i] = string(l)
	}
	fmt.Fprintf(b, "- Layers present: %s\n\n", joinOrNone(layers))
}

func writeInterfacesMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingInterfaces)
	if len(d.Nodes) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	for _, node := range d.Nodes {
		fmt.Fprintf(b, "### %s\n\n", node)
		rows := d.Interfaces[node]
		if len(rows) == 0 {
			fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
			continue
		}
		b.WriteString("| Kind | Name | Addresses | MTU | Up | Detail |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s |\n",
				r.Kind, r.Name, mdCell(r.Addresses), r.MTU, boolCell(r.Up), mdCell(r.Detail))
		}
		b.WriteString("\n")
	}
}

func writeVlanMatrixMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingVlanMatrix)
	if len(d.VLANs) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	b.WriteString("| VLAN | " + strings.Join(d.Nodes, " | ") + " |\n")
	b.WriteString("|---|" + strings.Repeat("---|", len(d.Nodes)) + "\n")
	for _, row := range d.VLANs {
		fmt.Fprintf(b, "| %d |", row.VID)
		for _, node := range d.Nodes {
			ifaces := row.Nodes[node]
			if len(ifaces) == 0 {
				b.WriteString(" - |")
			} else {
				fmt.Fprintf(b, " %s |", strings.Join(ifaces, ", "))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeSDNMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingSDN)
	if d.SDNErr != "" {
		fmt.Fprintf(b, "_SDN inventory unavailable: %s_\n\n", d.SDNErr)
		return
	}
	if len(d.SDN.Zones) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	for _, z := range d.SDN.Zones {
		fmt.Fprintf(b, "### Zone %s (%s)\n\n", z.ID, z.Type)
		fmt.Fprintf(b, "- Nodes: %s\n", joinOrNone(z.Nodes))
		if z.Bridge != "" {
			fmt.Fprintf(b, "- Bridge: %s\n", z.Bridge)
		}
		b.WriteString("\n")
		for _, v := range z.Vnets {
			fmt.Fprintf(b, "#### VNet %s (tag %d)\n\n", v.ID, v.Tag)
			if v.Alias != "" {
				fmt.Fprintf(b, "- Alias: %s\n", v.Alias)
			}
			if len(v.Subnets) == 0 {
				b.WriteString("- Subnets: none\n\n")
				continue
			}
			b.WriteString("| Subnet | Gateway |\n|---|---|\n")
			for _, s := range v.Subnets {
				fmt.Fprintf(b, "| %s | %s |\n", s.CIDR, s.Gateway)
			}
			b.WriteString("\n")
		}
	}
}

func writeFirewallMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingFirewall)
	if len(d.Firewall) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	b.WriteString("| Scope | Ref | Enabled | Default in | Default out | Rules | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range d.Firewall {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %d | %s |\n",
			r.Scope, r.Ref, boolCell(r.Enabled), mdCell(r.DefaultIn), mdCell(r.DefaultOut),
			r.RuleCount, mdCell(strings.Join(r.Banners, "; ")))
	}
	b.WriteString("\n")
}

func writeLLDPMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingLLDP)
	if len(d.LLDP) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	b.WriteString("| Node | NIC | Switch | Port | PVID | Tagged VLANs | Speed | Stale |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, p := range d.LLDP {
		tagged := make([]string, len(p.TaggedVLANs))
		for i, v := range p.TaggedVLANs {
			tagged[i] = strconv.Itoa(v)
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			p.Node, p.NIC, mdCell(p.Switch), mdCell(p.Port), pvidCell(p.PVID),
			mdCell(strings.Join(tagged, ",")), mdCell(speedCell(p.SpeedMbps, p.SpeedDescr)), boolCell(p.Stale))
	}
	b.WriteString("\n")
}

// speedCell renders a PortRow's speed for display, preferring the
// human-readable descriptor (e.g. "10Gbase-T full duplex") LLDP sometimes
// supplies over the bare Mbps figure, falling back to Mbps when only that
// is known.
func speedCell(speedMbps int, speedDescr string) string {
	if speedDescr != "" {
		return speedDescr
	}
	if speedMbps > 0 {
		return strconv.Itoa(speedMbps) + " Mbps"
	}
	return ""
}

func mdCell(s string) string {
	if s == "" {
		return "-"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}

func boolCell(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func pvidCell(v int) string {
	if v == 0 {
		return "-"
	}
	return strconv.Itoa(v)
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
