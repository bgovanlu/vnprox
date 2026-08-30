// SPDX-License-Identifier: Apache-2.0

// The NetBox-style address list (docs/features/ipam.md §2): every occupied
// address as a row, with the contiguous free space between them collapsed
// into "N addresses free" range rows. This replaces the old colored-square
// allocation grid — it reads the same for a /30 or a /16 (the response is
// sparse, proportional to actual usage), surfaces who/what/source inline
// instead of behind a per-cell click, and puts the summary and conflicts
// first so what needs attention reads at a glance.
import { useMemo, useState } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import type { IpamCell, IpamCellState, IpamCounts, IpamFreeRange } from "../api/types";
import { stateChipClasses, stateLabel, stateSwatchClasses } from "./labels";
import { useIpamAllocationsQuery } from "./queries";
import { CellDetailDialog } from "./CellDetailDialog";

export interface AddressListProps {
  subnetCidr: string;
  readOnly?: boolean;
}

/** The filter applied to the list: "all" shows every occupied address plus
 * the free ranges; a specific state narrows to matching entries (and, for
 * "free", to the range rows). */
type Filter = "all" | IpamCellState;

const FILTERS: { key: Filter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "allocated", label: "Allocated" },
  { key: "observed", label: "Observed" },
  { key: "conflict", label: "Conflicts" },
  { key: "reserved", label: "Reserved" },
  { key: "free", label: "Free" },
];

/** ipSortKey makes IPv4 addresses lexically sortable (zero-padded octets) so
 * entry and free rows interleave in true numeric order; IPv6 falls back to
 * its own string form (already broadly ordered), a rare case in PVE SDN. */
function ipSortKey(ip: string): string {
  if (ip.includes(":")) return ip;
  return ip
    .split(".")
    .map((o) => o.padStart(3, "0"))
    .join(".");
}

type Row =
  | { kind: "entry"; key: string; sortKey: string; cell: IpamCell }
  | { kind: "free"; key: string; sortKey: string; range: IpamFreeRange };

function matchesSearch(cell: IpamCell, q: string): boolean {
  if (!q) return true;
  const needle = q.toLowerCase();
  return (
    cell.ip.toLowerCase().includes(needle) ||
    (cell.hostname ?? "").toLowerCase().includes(needle) ||
    (cell.mac ?? "").toLowerCase().includes(needle) ||
    (cell.vmid !== undefined && cell.vmid > 0 ? String(cell.vmid).includes(needle) : false)
  );
}

/** UtilizationStrip is the segmented summary bar + count legend
 * (docs/features/ipam.md §2). Segments are mutually exclusive and sum to the
 * subnet's usable-host count, so the bar is exact. */
function UtilizationStrip({ counts, onPick, active }: { counts: IpamCounts; onPick: (f: Filter) => void; active: Filter }) {
  const segments: { state: IpamCellState; count: number }[] = [
    { state: "allocated", count: counts.allocated },
    { state: "reserved", count: counts.reserved },
    { state: "observed", count: counts.observed },
    { state: "gateway", count: counts.gateway },
    { state: "conflict", count: counts.conflict },
  ];
  const total = segments.reduce((n, s) => n + s.count, 0) + counts.free;
  const used = total - counts.free;
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex h-3 overflow-hidden rounded-md border border-slate-200 bg-slate-100 dark:border-slate-700 dark:bg-slate-800">
        {segments.map((s) =>
          s.count > 0 ? (
            <div key={s.state} className={clsx("h-full", stateSwatchClasses[s.state])} style={{ width: `${String(pct(s.count))}%` }} title={`${stateLabel(s.state)}: ${String(s.count)}`} />
          ) : null,
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-600 dark:text-slate-400">
        {segments.map((s) => (
          <button
            key={s.state}
            type="button"
            onClick={() => { onPick(active === s.state ? "all" : s.state); }}
            className={clsx("inline-flex items-center gap-1.5 rounded px-1 hover:text-slate-900 dark:hover:text-slate-100", active === s.state && "font-semibold text-slate-900 dark:text-slate-100")}
          >
            <span className={clsx("h-2.5 w-2.5 rounded-sm", stateSwatchClasses[s.state])} />
            {stateLabel(s.state).replace(" (unallocated)", "")} <span className="font-medium text-slate-700 tabular-nums dark:text-slate-200">{s.count}</span>
          </button>
        ))}
        <span className="inline-flex items-center gap-1.5">
          <span className={clsx("h-2.5 w-2.5 rounded-sm", stateSwatchClasses.free)} />
          Free <span className="font-medium text-slate-700 tabular-nums dark:text-slate-200">{counts.free.toLocaleString()}</span>
        </span>
        <span className="ml-auto tabular-nums">
          Total <span className="font-medium text-slate-700 dark:text-slate-200">{total.toLocaleString()}</span> · {total > 0 ? Math.round((used / total) * 100) : 0}% used
        </span>
      </div>
    </div>
  );
}

function EntryRow({ cell, onOpen }: { cell: IpamCell; onOpen: (cell: IpamCell) => void }) {
  const desc = cell.vmid !== undefined && cell.vmid > 0 ? `vm/${String(cell.vmid)}` : cell.guestRef ?? "";
  return (
    <button
      type="button"
      onClick={() => { onOpen(cell); }}
      aria-label={`${cell.ip}: ${stateLabel(cell.state)}`}
      className="grid w-full grid-cols-[minmax(120px,168px)_104px_1fr_auto] items-center gap-3 px-4 py-2 text-left text-sm hover:bg-slate-50 dark:hover:bg-slate-800/60"
    >
      <span className="flex items-center gap-2.5 font-mono font-medium tabular-nums">
        <span className={clsx("h-5 w-1 rounded-sm", stateSwatchClasses[cell.state])} />
        {cell.ip}
      </span>
      <span className={clsx("w-fit rounded-full px-2 py-0.5 text-[11px] font-semibold", stateChipClasses[cell.state])}>
        {stateLabel(cell.state).replace(" (unallocated)", "")}
      </span>
      <span className="min-w-0 truncate">
        {cell.hostname ? (
          <>
            {cell.hostname}
            {desc && <span className="text-slate-600 dark:text-slate-400"> · {desc}</span>}
          </>
        ) : (
          <span className="text-slate-600 dark:text-slate-400">{cell.state === "observed" ? "unknown host — not in IPAM" : desc || "—"}</span>
        )}
      </span>
      <span className="hidden justify-end font-mono text-[11px] text-slate-600 sm:flex dark:text-slate-400">
        {cell.mac ?? (cell.sources && cell.sources.length > 0 ? cell.sources.join(" · ") : "")}
      </span>
    </button>
  );
}

function FreeRow({ range, readOnly, onReserve }: { range: IpamFreeRange; readOnly?: boolean; onReserve: (ip: string) => void }) {
  return (
    <div className="grid grid-cols-[minmax(120px,168px)_1fr_auto] items-center gap-3 bg-slate-50/60 px-4 py-1.5 text-sm dark:bg-slate-900/40">
      <span className="flex items-center gap-2.5 font-mono text-slate-600 tabular-nums dark:text-slate-400">
        <span className="h-5 w-1 rounded-sm bg-slate-200 dark:bg-slate-700" />
        {range.start} – {range.end}
      </span>
      <span className="text-xs text-slate-600 dark:text-slate-400">
        <span className="font-semibold text-slate-700 tabular-nums dark:text-slate-200">{range.count.toLocaleString()}</span> address{range.count === 1 ? "" : "es"} free
      </span>
      {!readOnly && (
        <Button variant="ghost" size="sm" onClick={() => { onReserve(range.start); }}>
          Reserve first free →
        </Button>
      )}
    </div>
  );
}

/** Renders subnetCidr's address list: the summary strip, conflict callouts,
 * a filter/search toolbar, and the interleaved occupied/free rows, with the
 * reserve/release workflow driven off any row. */
export function AddressList({ subnetCidr, readOnly }: AddressListProps) {
  const { data, isLoading, isError, refetch } = useIpamAllocationsQuery(subnetCidr);
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  // The address whose detail/reserve dialog is open. A real occupied entry
  // carries its full Cell; reserving into free space passes a synthetic free
  // Cell so the dialog offers the reserve form.
  const [dialogCell, setDialogCell] = useState<IpamCell | undefined>(undefined);

  const rows = useMemo<Row[]>(() => {
    if (!data) return [];
    const out: Row[] = [];
    const showFree = filter === "all" || filter === "free";
    if (filter !== "free") {
      for (const cell of data.entries) {
        if (filter !== "all" && cell.state !== filter) continue;
        if (!matchesSearch(cell, search)) continue;
        out.push({ kind: "entry", key: `e:${cell.ip}`, sortKey: ipSortKey(cell.ip), cell });
      }
    }
    if (showFree && !search) {
      for (const range of data.freeRanges) {
        out.push({ kind: "free", key: `f:${range.start}`, sortKey: ipSortKey(range.start), range });
      }
    }
    out.sort((a, b) => (a.sortKey < b.sortKey ? -1 : a.sortKey > b.sortKey ? 1 : 0));
    return out;
  }, [data, filter, search]);

  if (isLoading) {
    return <p className="text-sm text-slate-600 dark:text-slate-400">Loading addresses…</p>;
  }
  if (isError || !data) {
    return (
      <EmptyState
        icon="sdn-subnet"
        variant="failed"
        title="Could not load addresses"
        description="Check that vnproxd can reach the local PVE API, then reload."
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  const firstFree = data.freeRanges[0]?.start;

  return (
    <div className="flex flex-col gap-4">
      <p className="flex items-center gap-1.5 text-xs font-medium text-slate-600 dark:text-slate-400">
        Addresses in {subnetCidr}
        <HelpAnchor topic="ipam-address-list" />
      </p>
      <UtilizationStrip counts={data.counts} onPick={setFilter} active={filter} />

      {data.conflicts.length > 0 && (
        <div className="flex flex-col gap-2">
          {data.conflicts.map((c) => (
            <div
              key={`${c.type}:${c.ips.join(",")}`}
              className={clsx(
                "rounded-lg border px-3 py-2 text-xs",
                c.severity === "error"
                  ? "border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-950/40 dark:text-red-200"
                  : "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200",
              )}
            >
              <p className="font-semibold">{c.message}</p>
              <p className="opacity-90">Suggested: {c.suggestion}</p>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <input
          type="search"
          value={search}
          onChange={(e) => { setSearch(e.target.value); }}
          placeholder="Filter by IP, hostname, MAC, or VMID…"
          aria-label="Filter addresses"
          className="min-w-[180px] flex-1 rounded-lg border border-slate-200 bg-slate-50 px-3 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700 dark:bg-slate-800"
        />
        <div className="flex flex-wrap gap-1.5">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              type="button"
              onClick={() => { setFilter(f.key); }}
              aria-pressed={filter === f.key}
              className={clsx(
                "rounded-full border px-2.5 py-1 text-xs font-medium transition-colors",
                filter === f.key
                  ? "border-accent-500 bg-accent-soft text-accent-fg"
                  : "border-slate-200 text-slate-600 hover:bg-slate-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800/60",
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
        {!readOnly && firstFree && (
          <Button variant="primary" size="sm" onClick={() => { setDialogCell({ ip: firstFree, state: "free" }); }}>
            + Reserve next free
          </Button>
        )}
      </div>

      <div className="overflow-hidden rounded-lg border border-slate-200 dark:border-slate-800">
        {rows.length === 0 ? (
          <p className="px-4 py-6 text-center text-sm text-slate-600 dark:text-slate-400">No addresses match this filter.</p>
        ) : (
          <div className="divide-y divide-slate-100 dark:divide-slate-800/80">
            {rows.map((row) =>
              row.kind === "entry" ? (
                <EntryRow key={row.key} cell={row.cell} onOpen={setDialogCell} />
              ) : (
                <FreeRow key={row.key} range={row.range} readOnly={readOnly} onReserve={(ip) => { setDialogCell({ ip, state: "free" }); }} />
              ),
            )}
          </div>
        )}
      </div>

      {dialogCell && (
        <CellDetailDialog
          open
          onOpenChange={(open) => {
            if (!open) setDialogCell(undefined);
          }}
          cell={dialogCell}
          subnetCidr={data.cidr}
          readOnly={readOnly}
        />
      )}
    </div>
  );
}
