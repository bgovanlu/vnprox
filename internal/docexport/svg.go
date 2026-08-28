// SPDX-License-Identifier: Apache-2.0

package docexport

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// point is a plotted entity's SVG-space center, recorded so edges can be
// drawn between two already-placed entities after every band is laid out.
type point struct{ x, y float64 }

// RenderSVG renders a deterministic, self-contained SVG mirroring
// docs/user-guide.md §2's documented topology map structure: "Each cluster
// node is a column; four layer bands stack within it: Physical (bottom),
// L2, SDN (spanning nodes), Guests (top)." It is not a re-implementation of
// the interactive map's elkjs force layout (that lives client-side, driven
// by @xyflow/react — docs/architecture.md §8) — this is a plain,
// deterministic column/band grid computed purely from the same projected
// topology.Topology the map itself renders, so the export never needs a
// browser or a second copy of the layout engine. It preserves the map's
// documented semantics (columns per node, stacked layer bands, edges drawn
// between them) rather than its pixel-exact force-directed positions.
//
// Nodes whose NodeGroup is "" (cluster-scoped: SDN zones/VNets, per
// topology.nodeGroupOf) are drawn in a full-width band of their own between
// the L2 and Guest bands, matching the user guide's "SDN: zones and VNets
// spanning nodes" description — they are not assigned to any one column.
func RenderSVG(t topology.Topology) string {
	return RenderSVGWithRegions(t, nil)
}

// RenderSVGWithRegions is RenderSVG plus T-2806's canvas regions, drawn as
// a labelled legend band beneath the diagram.
//
// A legend rather than positioned rectangles, deliberately: a region's
// x/y/w/h are in the interactive canvas's own graph space, and this export
// is explicitly NOT that layout (see RenderSVG's doc comment — it is a
// deterministic column/band grid, not the map's force-directed positions).
// Drawing the rectangles at their literal coordinates here would place them
// over unrelated entities and assert a grouping that isn't there. The
// legend carries the one thing that survives the change of layout: the
// operator's labels.
//
// The labels are operator free text and are escaped here, in this
// renderer, on their own — not by a helper shared with html.go/markdown.go
// (T-2806 AC6).
func RenderSVGWithRegions(t topology.Topology, regions []RegionRow) string {
	const (
		colWidth   = 240
		rowHeight  = 28
		bandGap    = 36
		colPadding = 20
		topMargin  = 50
		leftMargin = 20
		maxPerBand = 30 // cap so a large fixture can't blow the SVG up unboundedly
	)

	nodeCols := clusterColumns(t)
	sdnRows := bandRows(t, "", maxPerBand)

	pos := map[string]point{}

	width := leftMargin*2 + float64(len(nodeCols))*colWidth
	if width < 400 {
		width = 400
	}

	var body strings.Builder

	y := float64(topMargin)
	// Guests band (top).
	guestHeight := writeColumnBand(&body, t, nodeCols, string(topology.LayerGuest), y, colWidth, leftMargin, rowHeight, maxPerBand, pos)
	y += guestHeight + bandGap

	// SDN band: full width, one row of boxes (cluster-scoped entities).
	sdnHeight := writeFullWidthBand(&body, sdnRows, y, leftMargin, width-2*leftMargin, rowHeight, pos)
	y += sdnHeight + bandGap

	// L2 band.
	l2Height := writeColumnBand(&body, t, nodeCols, string(topology.LayerL2), y, colWidth, leftMargin, rowHeight, maxPerBand, pos)
	y += l2Height + bandGap

	// Physical band (bottom).
	physHeight := writeColumnBand(&body, t, nodeCols, string(topology.LayerPhysical), y, colWidth, leftMargin, rowHeight, maxPerBand, pos)
	y += physHeight + bandGap

	var legend strings.Builder
	legendHeight := writeRegionLegend(&legend, regions, y, leftMargin, rowHeight)

	height := y + legendHeight + 20

	var edges strings.Builder
	for _, e := range t.Edges {
		from, ok1 := pos[e.From]
		to, ok2 := pos[e.To]
		if !ok1 || !ok2 {
			continue
		}
		fmt.Fprintf(&edges, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" class="edge edge-%s"/>`+"\n",
			from.x, from.y, to.x, to.y, svgSafeClass(string(e.Kind)))
	}

	// Column headers (node names).
	var headers strings.Builder
	for i, node := range nodeCols {
		x := leftMargin + float64(i)*colWidth + colWidth/2
		fmt.Fprintf(&headers, `<text x="%.1f" y="%d" class="col-header" text-anchor="middle">%s</text>`+"\n",
			x, topMargin-20, html.EscapeString(node))
	}

	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.1f %.1f" width="%.1f" height="%.1f" font-family="sans-serif" font-size="11">`+"\n",
		width, height, width, height)
	svg.WriteString(`<style>
    .col-header { font-weight: bold; fill: #334155; }
    .band-label { font-style: italic; fill: #64748b; font-size: 10px; }
    .entity-box { fill: #e2e8f0; stroke: #94a3b8; stroke-width: 1; }
    .entity-box.status-down { fill: #fecaca; stroke: #dc2626; }
    .entity-box.status-degraded { fill: #fde68a; stroke: #d97706; }
    .entity-label { fill: #0f172a; }
    .edge { stroke: #94a3b8; stroke-width: 1; }
    .region-swatch { fill: #ede9fe; stroke: #7c3aed; stroke-width: 1; }
    .region-label { fill: #4c1d95; }
  </style>` + "\n")
	svg.WriteString(headers.String())
	svg.WriteString(edges.String())
	svg.WriteString(body.String())
	svg.WriteString(legend.String())
	svg.WriteString("</svg>")
	return svg.String()
}

// writeRegionLegend draws one swatch+label row per region and returns the
// vertical space it consumed (0 for no regions, so an export with no
// annotation layer is byte-identical to the pre-T-2806 one).
func writeRegionLegend(b *strings.Builder, regions []RegionRow, y, leftMargin, rowHeight float64) float64 {
	if len(regions) == 0 {
		return 0
	}
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="band-label">%s</text>`+"\n",
		leftMargin, y, html.EscapeString(strings.ToUpper(RegionsSubheading)))
	for i, r := range regions {
		ry := y + float64(i+1)*rowHeight
		fmt.Fprintf(b, `<rect x="%.1f" y="%.1f" width="16" height="12" rx="2" class="region-swatch"/>`+"\n",
			leftMargin, ry-10)
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="region-label">%s</text>`+"\n",
			leftMargin+24, ry, html.EscapeString(truncateLabel(r.Label)))
	}
	return float64(len(regions)+1) * rowHeight
}

// clusterColumns returns the sorted, distinct non-empty NodeGroup values in
// t — one topology map column per cluster node.
func clusterColumns(t topology.Topology) []string {
	set := map[string]bool{}
	for _, n := range t.Nodes {
		if n.NodeGroup != "" {
			set[n.NodeGroup] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// bandRows returns every Node in layer, in the given nodeGroup, sorted by
// label, capped at maxN.
func bandRows(t topology.Topology, nodeGroup string, maxN int) []topology.Node {
	var out []topology.Node
	for _, n := range t.Nodes {
		if string(n.Layer) != "" && n.NodeGroup == nodeGroup {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	if len(out) > maxN {
		out = out[:maxN]
	}
	return out
}

// writeColumnBand renders one layer band as a per-node-column stack of
// entity boxes, returns the band's total height (the tallest column).
func writeColumnBand(b *strings.Builder, t topology.Topology, cols []string, layer string, y float64, colWidth, leftMargin, rowHeight float64, maxPerBand int, pos map[string]point) float64 {
	maxHeight := rowHeight
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="band-label">%s</text>`+"\n", leftMargin, y-6, strings.ToUpper(layer))
	for i, col := range cols {
		var rows []topology.Node
		for _, n := range t.Nodes {
			if n.NodeGroup == col && string(n.Layer) == layer {
				rows = append(rows, n)
			}
		}
		sort.Slice(rows, func(a, c int) bool { return rows[a].Label < rows[c].Label })
		if len(rows) > maxPerBand {
			rows = rows[:maxPerBand]
		}
		x := leftMargin + float64(i)*colWidth
		for j, n := range rows {
			ry := y + float64(j)*rowHeight
			cls := "entity-box"
			switch n.Status {
			case topology.StatusDown:
				cls += " status-down"
			case topology.StatusDegraded:
				cls += " status-degraded"
			}
			fmt.Fprintf(b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" class="%s"/>`+"\n",
				x, ry, colWidth-16, rowHeight-6, cls)
			fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="entity-label">%s</text>`+"\n",
				x+6, ry+rowHeight-14, html.EscapeString(truncateLabel(n.Label)))
			pos[n.ID] = point{x + (colWidth-16)/2, ry + (rowHeight-6)/2}
		}
		if h := float64(len(rows)) * rowHeight; h > maxHeight {
			maxHeight = h
		}
	}
	return maxHeight
}

// writeFullWidthBand renders the SDN (cluster-scoped) band as a single row
// of boxes spanning the full canvas width rather than a per-node column.
func writeFullWidthBand(b *strings.Builder, rows []topology.Node, y float64, leftMargin, width, rowHeight float64, pos map[string]point) float64 {
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="band-label">SDN</text>`+"\n", leftMargin, y-6)
	if len(rows) == 0 {
		return rowHeight
	}
	boxWidth := 160.0
	perRow := int(width / boxWidth)
	if perRow < 1 {
		perRow = 1
	}
	maxRow := 0
	for i, n := range rows {
		col := i % perRow
		row := i / perRow
		if row > maxRow {
			maxRow = row
		}
		x := leftMargin + float64(col)*boxWidth
		ry := y + float64(row)*rowHeight
		fmt.Fprintf(b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" class="entity-box"/>`+"\n",
			x, ry, boxWidth-10, rowHeight-6)
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" class="entity-label">%s</text>`+"\n",
			x+6, ry+rowHeight-14, html.EscapeString(truncateLabel(n.Label)))
		pos[n.ID] = point{x + (boxWidth-10)/2, ry + (rowHeight-6)/2}
	}
	return float64(maxRow+1) * rowHeight
}

func truncateLabel(s string) string {
	const max = 24
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func svgSafeClass(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
