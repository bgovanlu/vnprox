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
	HeadingAnnotations = "Operator annotations"
	// RegionsSubheading names the canvas-regions subsection, and
	// OrphanedMarker is how a note whose entity is gone is flagged in both
	// text formats. Exported so the golden test asserts one string rather
	// than two renderer-specific ones.
	RegionsSubheading = "Canvas regions"
	OrphanedMarker    = "orphaned - entity no longer exists"
	// NeverExpiresMarker is the rendered form of the 0 "no expiry" sentinel.
	NeverExpiresMarker = "never"
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
	writeAnnotationsMD(&b, d)

	return b.String()
}

// writeAnnotationsMD renders T-2806's annotation layer: the notes and
// regions an operator wrote on the map, for the reader who cannot see it.
//
// Every operator-authored string on this path goes through mdText, never
// raw and never mdCell (which escapes only the table delimiter). A note is
// free text one operator typed and another reads inside a rendered
// document, which is the classic injection surface; see mdText.
func writeAnnotationsMD(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "## %s\n\n", HeadingAnnotations)
	if len(d.Annotations) == 0 && len(d.Regions) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}

	if len(d.Annotations) > 0 {
		b.WriteString("| Entity | Note | Author | Created | Expires | Status |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, a := range d.Annotations {
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
				mdText(a.Ref), mdText(a.Content), mdText(a.CreatedBy),
				mdText(a.Created), expiresCell(a.Expires), orphanCell(a.Orphaned))
		}
		b.WriteString("\n")
	}

	if len(d.Regions) > 0 {
		fmt.Fprintf(b, "### %s\n\n", RegionsSubheading)
		b.WriteString("| Region | Author | Created | Expires |\n|---|---|---|---|\n")
		for _, r := range d.Regions {
			fmt.Fprintf(b, "| %s | %s | %s | %s |\n",
				mdText(r.Label), mdText(r.CreatedBy), mdText(r.Created), expiresCell(r.Expires))
		}
		b.WriteString("\n")
	}
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

// mdTextReplacer neutralises operator-authored free text for the Markdown
// renderer (T-2806 AC6). Three distinct hazards, one pass:
//
//   - `&`, `<`, `>` become entities. CommonMark passes raw HTML straight
//     through to whatever renders the Markdown, so a note reading
//     `<script>...` would otherwise become a live script tag in every
//     downstream HTML view of this document. Entity-escaping keeps it a
//     sentence.
//   - `|` is escaped, or a note containing one silently invents table
//     columns and corrupts every following cell.
//   - newlines collapse to spaces: a multi-line note (the textarea allows
//     them) would otherwise terminate the table row mid-note and drop the
//     rest of the text out of the document entirely.
//
// Deliberately NOT shared with html.go's escaping: each renderer owns and
// is tested on its own escape, so a mutation to one cannot be masked by
// the other still being correct.
var mdTextReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"|", "\\|",
	"\r\n", " ",
	"\n", " ",
	"\r", " ",
)

func mdText(s string) string {
	if s == "" {
		return "-"
	}
	return mdTextReplacer.Replace(s)
}

// expiresCell renders an annotation's expiry, mapping "" (the 0 sentinel)
// to the explicit "never" rather than a bare dash: "this note has no
// expiry" is a fact worth stating in a document someone reads a year later.
func expiresCell(stamp string) string {
	if stamp == "" {
		return NeverExpiresMarker
	}
	return mdText(stamp)
}

func orphanCell(orphaned bool) string {
	if orphaned {
		return OrphanedMarker
	}
	return "-"
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
