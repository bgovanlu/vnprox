package docexport

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
)

// HTML renders d as a standalone HTML document (docs/features/blueprints.md
// §4: "standalone HTML with embedded topology SVG"). Standalone means
// exactly that: every byte the document needs is inline (a <style> block,
// no <script>, no <link>, no <img src="http...">, the topology diagram as
// an inline <svg>) — opening the file with no network at all renders
// identically. The golden test (build_test.go) greps the output for
// "http://"/"https://" in any src/href attribute as a CSP-style check for
// this property.
func HTML(d Data) string {
	var b strings.Builder

	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(docTitle))
	b.WriteString(htmlStyle)
	b.WriteString("</head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(docTitle))
	fmt.Fprintf(&b, "<p class=\"generated\">Generated: %s (%d)</p>\n",
		html.EscapeString(time.Unix(d.GeneratedAt, 0).UTC().Format(time.RFC3339)), d.GeneratedAt)

	writeTopologyHTML(&b, d)
	writeInterfacesHTML(&b, d)
	writeVlanMatrixHTML(&b, d)
	writeSDNHTML(&b, d)
	writeFirewallHTML(&b, d)
	writeLLDPHTML(&b, d)

	b.WriteString("</body></html>\n")
	return b.String()
}

const htmlStyle = `<style>
  body { font-family: -apple-system, "Segoe UI", sans-serif; margin: 2rem; color: #0f172a; background: #fff; }
  h1 { margin-bottom: 0.25rem; }
  h2 { margin-top: 2rem; border-bottom: 1px solid #cbd5e1; padding-bottom: 0.25rem; }
  h3 { margin-top: 1.25rem; }
  .generated { color: #64748b; font-size: 0.9rem; }
  table { border-collapse: collapse; width: 100%; margin: 0.5rem 0 1rem; font-size: 0.9rem; }
  th, td { border: 1px solid #cbd5e1; padding: 0.35rem 0.5rem; text-align: left; vertical-align: top; }
  th { background: #f1f5f9; }
  .none { color: #94a3b8; font-style: italic; }
  .topology-svg { border: 1px solid #cbd5e1; border-radius: 4px; max-width: 100%; overflow: auto; }
</style>
`

func writeTopologyHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingTopology))
	fmt.Fprintf(b, "<p>%d cluster nodes, %d topology elements, %d edges.</p>\n",
		len(d.Nodes), len(d.Topology.Nodes), len(d.Topology.Edges))
	b.WriteString("<div class=\"topology-svg\">\n")
	b.WriteString(RenderSVG(d.Topology))
	b.WriteString("\n</div>\n")
}

func writeInterfacesHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingInterfaces))
	if len(d.Nodes) == 0 {
		writeNoneHTML(b)
		return
	}
	for _, node := range d.Nodes {
		fmt.Fprintf(b, "<h3>%s</h3>\n", html.EscapeString(node))
		rows := d.Interfaces[node]
		if len(rows) == 0 {
			writeNoneHTML(b)
			continue
		}
		b.WriteString("<table><thead><tr><th>Kind</th><th>Name</th><th>Addresses</th><th>MTU</th><th>Up</th><th>Detail</th></tr></thead><tbody>\n")
		for _, r := range rows {
			fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>\n",
				html.EscapeString(r.Kind), html.EscapeString(r.Name), htmlCell(r.Addresses),
				r.MTU, boolCell(r.Up), htmlCell(r.Detail))
		}
		b.WriteString("</tbody></table>\n")
	}
}

func writeVlanMatrixHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingVlanMatrix))
	if len(d.VLANs) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<table><thead><tr><th>VLAN</th>")
	for _, node := range d.Nodes {
		fmt.Fprintf(b, "<th>%s</th>", html.EscapeString(node))
	}
	b.WriteString("</tr></thead><tbody>\n")
	for _, row := range d.VLANs {
		fmt.Fprintf(b, "<tr><td>%d</td>", row.VID)
		for _, node := range d.Nodes {
			ifaces := row.Nodes[node]
			if len(ifaces) == 0 {
				b.WriteString("<td class=\"none\">-</td>")
			} else {
				fmt.Fprintf(b, "<td>%s</td>", html.EscapeString(strings.Join(ifaces, ", ")))
			}
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table>\n")
}

func writeSDNHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingSDN))
	if d.SDNErr != "" {
		fmt.Fprintf(b, "<p class=\"none\">SDN inventory unavailable: %s</p>\n", html.EscapeString(d.SDNErr))
		return
	}
	if len(d.SDN.Zones) == 0 {
		writeNoneHTML(b)
		return
	}
	for _, z := range d.SDN.Zones {
		fmt.Fprintf(b, "<h3>Zone %s (%s)</h3>\n", html.EscapeString(z.ID), html.EscapeString(z.Type))
		fmt.Fprintf(b, "<p>Nodes: %s", html.EscapeString(joinOrNone(z.Nodes)))
		if z.Bridge != "" {
			fmt.Fprintf(b, " &middot; Bridge: %s", html.EscapeString(z.Bridge))
		}
		b.WriteString("</p>\n")
		for _, v := range z.Vnets {
			fmt.Fprintf(b, "<h4>VNet %s (tag %d)%s</h4>\n", html.EscapeString(v.ID), v.Tag, vnetAliasSuffix(v.Alias))
			if len(v.Subnets) == 0 {
				b.WriteString("<p class=\"none\">no subnets</p>\n")
				continue
			}
			b.WriteString("<table><thead><tr><th>Subnet</th><th>Gateway</th></tr></thead><tbody>\n")
			for _, s := range v.Subnets {
				fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td></tr>\n", html.EscapeString(s.CIDR), html.EscapeString(s.Gateway))
			}
			b.WriteString("</tbody></table>\n")
		}
	}
}

func vnetAliasSuffix(alias string) string {
	if alias == "" {
		return ""
	}
	return " &mdash; " + html.EscapeString(alias)
}

func writeFirewallHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingFirewall))
	if len(d.Firewall) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<table><thead><tr><th>Scope</th><th>Ref</th><th>Enabled</th><th>Default in</th><th>Default out</th><th>Rules</th><th>Notes</th></tr></thead><tbody>\n")
	for _, r := range d.Firewall {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>\n",
			html.EscapeString(r.Scope), html.EscapeString(r.Ref), boolCell(r.Enabled),
			htmlCell(r.DefaultIn), htmlCell(r.DefaultOut), r.RuleCount, htmlCell(strings.Join(r.Banners, "; ")))
	}
	b.WriteString("</tbody></table>\n")
}

func writeLLDPHTML(b *strings.Builder, d Data) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingLLDP))
	if len(d.LLDP) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<table><thead><tr><th>Node</th><th>NIC</th><th>Switch</th><th>Port</th><th>PVID</th><th>Tagged VLANs</th><th>Speed</th><th>Stale</th></tr></thead><tbody>\n")
	for _, p := range d.LLDP {
		tagged := make([]string, len(p.TaggedVLANs))
		for i, v := range p.TaggedVLANs {
			tagged[i] = strconv.Itoa(v)
		}
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(p.Node), html.EscapeString(p.NIC), htmlCell(p.Switch), htmlCell(p.Port),
			htmlCell(pvidCell(p.PVID)), htmlCell(strings.Join(tagged, ",")),
			htmlCell(speedCell(p.SpeedMbps, p.SpeedDescr)), boolCell(p.Stale))
	}
	b.WriteString("</tbody></table>\n")
}

func writeNoneHTML(b *strings.Builder) {
	fmt.Fprintf(b, "<p class=\"none\">none observed</p>\n")
}

func htmlCell(s string) string {
	if s == "" {
		return "-"
	}
	return html.EscapeString(s)
}
