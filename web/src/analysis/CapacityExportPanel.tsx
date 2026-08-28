// SPDX-License-Identifier: Apache-2.0

// The capacity history export (GET /capacity/export, T-1606).
//
// This is an *export*, not a forecast. Capacity forecasts already reach the
// operator as `SourceCapacity` findings in the unified findings stream, so
// there is deliberately no second forecast screen here — the headless thing
// was the per-entity history download, and this is it.
//
// Two properties the panel must not lose:
//
//   1. Both required query parameters (`ref` and `kind`) are always sent.
//      The daemon answers `400 validation_failed` without either, and the
//      two kinds do not share a ref scheme, so a picker that let them drift
//      apart would produce a confidently wrong URL.
//   2. The retention bound is stated next to the download. The export is
//      clamped server-side to `[capacity] aggregate_retention_days`, and
//      that value is not on `GET /config` — so the note names the setting
//      and its default without claiming to know this daemon's value.
import { useMemo, useState } from "react";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import { RETENTION_BOUND_NOTE, capacityExportCsvHref } from "../api/capacity";
import type { CapacityKind } from "../api/types";
import { useIpamSubnetsQuery } from "../ipam/queries";
import { useTopologyQuery } from "../topology/queries";
import { linkEntities, poolEntities, type CapacityEntity } from "./capacityEntities";
import { useCapacityExportQuery } from "./analysisQueries";

const KIND_LABEL: Readonly<Record<CapacityKind, string>> = {
  link: "Link (physical NIC)",
  ipam_pool: "IPAM pool",
};

export function CapacityExportPanel() {
  const [kind, setKind] = useState<CapacityKind>("link");
  const [ref, setRef] = useState("");

  const topologyQuery = useTopologyQuery();
  const subnetsQuery = useIpamSubnetsQuery();

  const links = useMemo(() => linkEntities(topologyQuery.data?.nodes ?? []), [topologyQuery.data]);
  const pools = useMemo(() => poolEntities(subnetsQuery.data?.items ?? []), [subnetsQuery.data]);
  const entities: CapacityEntity[] = kind === "link" ? links : pools;

  // The picker's own value: whatever was selected if it is still in range
  // for the current kind, otherwise the first candidate. Kept derived rather
  // than synced in an effect so switching kind can never leave a link ref
  // paired with kind=ipam_pool.
  const selected = entities.find((e) => e.ref === ref) ?? entities[0];

  return (
    <section aria-labelledby="capacity-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="capacity-heading" className="flex items-center gap-2 text-lg font-semibold">
          Capacity history export
          <HelpAnchor topic="capacity-export" />
        </h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          One link&apos;s or one IPAM pool&apos;s daily utilization history, as CSV or JSON. Capacity{" "}
          <em>forecasts</em> are not here — they arrive as findings in the findings stream, where a projected crossing
          can be acknowledged like any other. This is the raw history behind them.
        </p>
      </div>

      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1 text-xs">
          Entity kind
          <select
            value={kind}
            onChange={(e) => {
              setKind(e.target.value === "ipam_pool" ? "ipam_pool" : "link");
              setRef("");
            }}
            className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="link">{KIND_LABEL.link}</option>
            <option value="ipam_pool">{KIND_LABEL.ipam_pool}</option>
          </select>
        </label>

        <label className="flex flex-col gap-1 text-xs">
          Entity
          <select
            aria-label="Entity to export"
            value={selected?.ref ?? ""}
            disabled={entities.length === 0}
            onChange={(e) => {
              setRef(e.target.value);
            }}
            className="min-w-56 rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          >
            {entities.map((e) => (
              <option key={e.ref} value={e.ref}>
                {e.label}
              </option>
            ))}
          </select>
        </label>

        {selected && (
          // A plain <a download> rather than an apiFetch round trip: the CSV
          // comes back as an attachment and a same-origin navigation already
          // carries the session cookie — the same shape ToolsPage's
          // documentation export uses.
          <a
            href={capacityExportCsvHref(selected.ref, kind)}
            download
            data-testid="capacity-export-csv"
            className="inline-flex h-8 items-center justify-center rounded-md bg-slate-200 px-3 text-xs font-medium text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
          >
            Download CSV
          </a>
        )}
      </div>

      <p data-testid="capacity-retention-note" className="text-xs text-slate-600 dark:text-slate-400">
        {RETENTION_BOUND_NOTE}
      </p>

      {entities.length === 0 ? (
        <EmptyState
          icon={kind === "link" ? "physnic" : "sdn-subnet"}
          variant="empty"
          title={kind === "link" ? "No speed-bearing links discovered" : "No sized IPAM pools"}
          description={
            kind === "link"
              ? "Capacity history is rolled up only for physical NICs with a negotiated link speed — without one there is no utilization percentage to record. Bonds are not rolled up individually."
              : "A pool needs a nonzero size before its consumption is rolled up."
          }
          density="compact"
        />
      ) : (
        selected && <ExportPreview entityRef={selected.ref} kind={kind} />
      )}
    </section>
  );
}

/** The JSON form of exactly what the CSV download contains — so an operator
 * can see whether there is any history before downloading an empty file. */
function ExportPreview({ entityRef, kind }: { entityRef: string; kind: CapacityKind }) {
  const { data, isLoading, error, refetch } = useCapacityExportQuery(entityRef, kind);

  if (isLoading) return <p className="text-sm text-slate-600 dark:text-slate-400">Loading history…</p>;
  if (error) {
    return (
      <EmptyState
        icon={kind === "link" ? "physnic" : "sdn-subnet"}
        variant="failed"
        title="Could not read this entity's history"
        description="The daemon rejected or could not answer the export request. The entity may not be one it rolls up."
        density="compact"
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }
  if (!data || data.aggregates.length === 0) {
    return (
      <EmptyState
        icon={kind === "link" ? "physnic" : "sdn-subnet"}
        variant="empty"
        title="No history within the retention window"
        description="Nothing has been rolled up for this entity inside the retention bound. The daily rollup writes one bucket per complete UTC day, so a recently-discovered entity has none yet."
        density="compact"
      />
    );
  }

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold">
        {data.aggregates.length} daily bucket{data.aggregates.length === 1 ? "" : "s"}
      </h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Day (UTC)</TableHead>
            <TableHead>Average utilization</TableHead>
            <TableHead>Peak utilization</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.aggregates.map((a) => (
            <TableRow key={a.bucketAt}>
              <TableCell className="font-mono text-xs">{formatDay(a.bucketAt)}</TableCell>
              <TableCell className="tabular-nums">{formatPct(a.avgUtilization)}</TableCell>
              <TableCell className="tabular-nums">{formatPct(a.maxUtilization)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/** Buckets are stamped at 00:00 UTC of the day they cover, so they are
 * rendered in UTC — rendering them in the browser's zone would move a
 * bucket onto the wrong calendar day west of Greenwich. */
function formatDay(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toISOString().slice(0, 10);
}

function formatPct(value: number): string {
  return `${value.toFixed(1)}%`;
}
