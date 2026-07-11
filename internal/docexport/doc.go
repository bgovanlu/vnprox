// Package docexport builds the "as-built" config documentation export
// (docs/features/blueprints.md §4: "One click -> Markdown/HTML document of
// the cluster network: rendered topology (SVG), per-node interface tables,
// VLAN matrix, SDN inventory, firewall summaries, LLDP wiring table.
// Timestamped."). It is a pure, read-only reporting layer: every fact it
// renders is read straight from the same live sources every other read API
// already exposes (internal/topology, internal/sdn, internal/fw, the
// inventory snapshot) — nothing here is persisted, and nothing here is a
// second copy of PVE-owned truth (CLAUDE.md: "vnprox's SQLite store holds
// only app-owned data ... never persist a shadow copy of PVE config").
//
// Gather assembles one format-agnostic Data value from those sources;
// Markdown and HTML are both pure functions of that one Data, so the two
// exported formats can never structurally drift from each other — only
// presentation differs. The HTML form additionally embeds an inline <svg>
// topology render (svg.go) so it is fully self-contained: no external
// requests of any kind (the "standalone HTML" requirement), safe to open
// from a local file or a locked-down environment.
package docexport
