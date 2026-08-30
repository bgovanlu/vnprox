// SPDX-License-Identifier: Apache-2.0

// T-3907: the physical cabling plan — for every node, its physical NICs,
// the far-end switch/port LLDP reports (chassis name/ID, port ID/
// description — internal/host/lldp.go's already-parsed LLDPNeighbor), and
// an explicit "not discovered" state for NICs LLDP has no neighbor for.
// Rendering/print-layout only: no new host read, no new backend route —
// this consumes the same GET /topology payload TopologyPage/SwitchView
// already fetch, re-projected through switchModel.ts's existing
// buildSwitchModel (which already joins every physnic against its LLDP
// neighbor) and then flattened per-node by cablingPlan.ts's
// buildCablingPlan.
//
// Printable output: a print stylesheet (Tailwind's `print:` variant,
// mirroring TopologyPage.tsx's T-906 convention) rather than a second,
// docexport-based export path. internal/docexport already renders an
// "LLDP wiring" table, but only for NICs that HAVE a discovered neighbor
// (topology.Ports only ever emits a PortRow from an existing LldpNeighbor
// entity) — it has no path to enumerate a NIC with none, which is this
// card's central accessibility requirement ("unknown" must not read as
// "not connected"). switchModel.ts's join already carries every physnic
// either way, so extending it here (client-side, one join, already
// fetched) is less machinery than teaching the Go side a second, parallel
// "enumerate every physnic and left-join LLDP" builder just to duplicate
// what this file already has for free. The accessible table below is the
// page's actual content — printed as-is by the browser's native print —
// and the SVG patch-panel diagram underneath it is a purely visual,
// `aria-hidden` enhancement (never the only place a fact appears),
// rendered as real SVG elements (not injected markup) through the app's
// own Tailwind `fill-*`/`stroke-*` tokens, matching this codebase's
// standing avoidance of dangerouslySetInnerHTML (see help/inline.ts).
import { useMemo } from "react";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import { PageHeader } from "../components/PageHeader";
import { useTopologyQuery } from "../topology/queries";
import { buildSwitchModel } from "../topology/switchModel";
import {
  buildCablingPlan,
  cablingPlanRowCount,
  cablingPlanUnknownCount,
  computeCablingDiagramLayout,
  type CablingDiagramPort,
  type CablingRow,
} from "../topology/cablingPlan";

const th = "px-2 py-1.5 text-left font-medium text-fg-subtle";
const td = "px-2 py-1.5 align-top";

function speedLabel(row: CablingRow): string {
  if (!row.speedMbps) return "";
  return row.speedMbps >= 1000 ? `${String(row.speedMbps / 1000)} Gbps` : `${String(row.speedMbps)} Mbps`;
}

/** The far-end cell: the one place this view must not let "unknown" read as
 * "blank"/"not connected" (task card's central accessibility requirement).
 * Each of the three states gets its own, differently-worded, non-blank
 * text, and only the genuinely unknown state gets the amber "not
 * discovered" treatment — a grouped pill is a display artifact of
 * collapsing, not an unresolved link, so it reads as an instruction
 * ("view individually") rather than a warning. */
function FarEndCell({ row }: { row: CablingRow }) {
  if (row.linkState === "discovered") {
    return (
      <span className="text-fg-body">
        {row.farEndSwitch ?? "(unnamed switch)"}
        {row.farEndPort ? ` · ${row.farEndPort}` : ""}
      </span>
    );
  }
  if (row.linkState === "grouped") {
    return (
      <span className="text-fg-muted">
        {row.groupCount ?? "several"} NICs grouped — view individually on the Topology map
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-semibold text-amber-800 dark:bg-amber-900/50 dark:text-amber-200">
      Not discovered
    </span>
  );
}

/** Fill/stroke classes per link state — dark: variants included, same
 * `text-fg-muted` family the rest of the app uses for
 * muted text (T-3907 gate note), applied here via Tailwind's `fill-*`
 * utilities since these are SVG text nodes, not HTML. */
const PORT_BOX_CLASS: Record<CablingDiagramPort["linkState"], string> = {
  discovered: "fill-slate-50 stroke-slate-300 dark:fill-slate-800 dark:stroke-slate-600",
  unknown: "fill-amber-50 stroke-amber-600 [stroke-dasharray:4_3] dark:fill-slate-800 dark:stroke-amber-500",
  grouped: "fill-slate-50 stroke-slate-400 [stroke-dasharray:4_3] dark:fill-slate-800 dark:stroke-slate-500",
};

function DiagramPortBox({ port }: { port: CablingDiagramPort }) {
  return (
    <g data-cabling-entity="port" data-entity-ref={port.ref} data-link-state={port.linkState}>
      <rect
        x={port.x}
        y={port.y}
        width={port.width}
        height={port.height}
        rx={4}
        strokeWidth={1.5}
        className={PORT_BOX_CLASS[port.linkState]}
      />
      <text
        x={port.x + 6}
        y={port.y + 16}
        fontSize={10}
        fontWeight={600}
        className="fill-slate-900 dark:fill-slate-100"
      >
        {port.label}
      </text>
      <text x={port.x + 6} y={port.y + 32} fontSize={9} className="fill-slate-600 dark:fill-slate-400">
        {port.farEndLabel}
      </text>
    </g>
  );
}

export function CablingPlanView() {
  const { data, isLoading, isError } = useTopologyQuery();

  const plan = useMemo(() => {
    if (!data) return { nodes: [] };
    return buildCablingPlan(buildSwitchModel(data.nodes, data.edges));
  }, [data]);

  const diagram = useMemo(() => computeCablingDiagramLayout(plan), [plan]);
  // Not memoized on `data`: this is just the caption's "as of" timestamp,
  // not something that must stay pinned across unrelated re-renders.
  const generatedAt = new Date();

  const rowCount = cablingPlanRowCount(plan);
  const unknownCount = cablingPlanUnknownCount(plan);

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <PageHeader
        title={
          <>
            Cabling plan <HelpAnchor topic="cabling-plan-page" />
          </>
        }
        description="Every physical NIC in the cluster and the switch/port LLDP reports for it — the wiring diagram every homelab and small datacentre draws by hand and then lets go stale. LLDP names a far end's identity and port, never a rack position or a cable length or colour; there is no rack elevation here because none of that is knowable from the wire."
        actions={
          <Button variant="secondary" shape="pill" size="sm" className="print:hidden" onClick={() => { window.print(); }}>
            Print
          </Button>
        }
      />

      {isLoading && <p className="text-sm text-fg-muted">Loading the cabling plan…</p>}
      {isError && <p className="text-sm text-red-600 dark:text-red-400">Could not load the topology data behind this plan.</p>}

      {data && rowCount === 0 && (
        <EmptyState
          icon="physnic"
          variant="empty"
          title="No physical NICs discovered yet"
          description="Nothing in the current topology carries a physnic entity. If nodes are visible on the map but their NICs aren't, check the host collector's status."
        />
      )}

      {rowCount > 0 && (
        <>
          <p className="text-sm text-fg-muted">
            {rowCount} {rowCount === 1 ? "NIC" : "NICs"} across {plan.nodes.length}{" "}
            {plan.nodes.length === 1 ? "node" : "nodes"}
            {unknownCount > 0
              ? ` — ${String(unknownCount)} with no LLDP neighbor discovered (an unmanaged switch, a directly-attached host, or LLDP disabled on the far end are all common causes).`
              : " — every NIC has a discovered LLDP neighbor."}
          </p>

          <div className="space-y-6">
            {plan.nodes.map((group) => (
              <section key={group.node} aria-label={`Cabling for ${group.node}`} className="space-y-2">
                <h2 className="text-base font-semibold text-fg">{group.node}</h2>
                <div className="overflow-x-auto rounded-lg border border-border">
                  <table className="w-full border-collapse text-sm">
                    <thead className="border-b border-border bg-slate-50 dark:bg-slate-900">
                      <tr>
                        <th className={th}>NIC</th>
                        <th className={th}>Attached to</th>
                        <th className={th}>Media</th>
                        <th className={th}>Speed / duplex</th>
                        <th className={th}>Far end (switch · port)</th>
                      </tr>
                    </thead>
                    <tbody>
                      {group.rows.map((row) => (
                        <tr
                          key={row.nicRef}
                          className="border-b border-slate-100 last:border-b-0 dark:border-slate-800"
                        >
                          <td className={`${td} font-medium text-fg-body`}>{row.nicLabel}</td>
                          <td className={`${td} text-fg-body`}>
                            {row.bridgeName ?? <span className="text-fg-muted">(unattached)</span>}
                            {row.bondLabel ? ` via ${row.bondLabel}` : ""}
                          </td>
                          <td className={`${td} text-fg-body`}>{row.mediaPort ?? ""}</td>
                          <td className={`${td} text-fg-body`}>
                            {[speedLabel(row), row.duplex].filter(Boolean).join(" ") || ""}
                          </td>
                          <td className={td}>
                            <FarEndCell row={row} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            ))}
          </div>

          {/* Enhancement only — the table above is the accessible source of
           * truth (task card's explicit requirement). aria-hidden so this
           * never becomes a second, screen-reader-visible copy of the same
           * facts; its own text still clears AA contrast (see the
           * PORT_BOX_CLASS/text classes above) since aria-hidden only
           * removes it from the accessibility tree, not from what a
           * sighted/low-vision reader (including on a printed page) sees. */}
          <div aria-hidden="true" className="overflow-x-auto rounded-lg border border-border p-2">
            <svg
              width={diagram.width}
              height={diagram.height}
              viewBox={`0 0 ${String(diagram.width)} ${String(diagram.height)}`}
              className="font-sans"
              data-cabling-node-count={diagram.rows.length}
            >
              {diagram.rows.map((row) => (
                <g key={row.node}>
                  <text
                    x={row.labelX}
                    y={row.labelY}
                    fontSize={12}
                    fontWeight={600}
                    className="fill-slate-900 dark:fill-slate-100"
                  >
                    {row.node}
                  </text>
                  {row.ports.map((port) => (
                    <DiagramPortBox key={port.ref} port={port} />
                  ))}
                </g>
              ))}
            </svg>
          </div>

          <p className="text-xs text-fg-muted">Generated {generatedAt.toLocaleString()}</p>
        </>
      )}
    </div>
  );
}
