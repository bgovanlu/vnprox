// SPDX-License-Identifier: Apache-2.0

// T-1305's Conntrack & NAT table explorer: a live, filterable table over
// GET /conntrack (docs/api.md's Conntrack section) — the "what is this
// connection doing right now" complement to T-1003's Flow Explorer
// (flows/FlowExplorer.tsx), whose filter-control shape this page reuses.
// Read-only — no flush/delete affordance anywhere on this page, on
// purpose (docs/features's "read-only this arc" contract; this task's
// completion report states the grep-verifiable regression check).
import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { protoName } from "../flows/proto";
import type { ConntrackEntry, NatAddr } from "../api/types";
import type { ConntrackFilter } from "../api/conntrack";
import { useConntrackQuery } from "./conntrackQueries";
import { decodeConntrackExplorerState, encodeConntrackExplorerState } from "./urlState";

function natLabel(nat: NatAddr | undefined): string {
  if (!nat) return "—";
  return nat.port ? `${nat.ip}:${String(nat.port)}` : nat.ip;
}

function endpoint(ip: string, port: number | undefined): string {
  return port !== undefined ? `${ip}:${String(port)}` : ip;
}

function formatTimeout(sec: number | undefined): string {
  if (sec === undefined) return "—";
  if (sec < 60) return `${String(sec)}s`;
  if (sec < 3600) return `${String(Math.round(sec / 60))}m`;
  return `${String(Math.round(sec / 3600))}h`;
}

interface FilterBarProps {
  filter: ConntrackFilter;
  onChange: (patch: Partial<ConntrackFilter>) => void;
}

function FilterBar({ filter, onChange }: FilterBarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input
        aria-label="Filter by node"
        placeholder="node"
        value={filter.node ?? ""}
        onChange={(e) => { onChange({ node: e.target.value || undefined }); }}
        className="w-28 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
      <input
        aria-label="Filter by guest ref"
        placeholder="guest ref"
        value={filter.guest ?? ""}
        onChange={(e) => { onChange({ guest: e.target.value || undefined }); }}
        className="w-48 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
      <input
        aria-label="Filter by source IP"
        placeholder="src IP"
        value={filter.srcIp ?? ""}
        onChange={(e) => { onChange({ srcIp: e.target.value || undefined }); }}
        className="w-32 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
      <input
        aria-label="Filter by destination IP"
        placeholder="dst IP"
        value={filter.dstIp ?? ""}
        onChange={(e) => { onChange({ dstIp: e.target.value || undefined }); }}
        className="w-32 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
      <input
        aria-label="Filter by port"
        placeholder="port"
        inputMode="numeric"
        value={filter.port !== undefined ? String(filter.port) : ""}
        onChange={(e) => {
          const v = e.target.value;
          onChange({ port: v && Number.isFinite(Number(v)) ? Number(v) : undefined });
        }}
        className="w-20 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
      <input
        aria-label="Filter by state"
        placeholder="state (e.g. ESTABLISHED)"
        value={filter.state ?? ""}
        onChange={(e) => { onChange({ state: e.target.value || undefined }); }}
        className="w-44 rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900"
      />
    </div>
  );
}

export function ConntrackExplorer() {
  const [searchParams, setSearchParams] = useSearchParams();
  const initial = useMemo(() => decodeConntrackExplorerState(searchParams), []); // eslint-disable-line react-hooks/exhaustive-deps

  const [filter, setFilter] = useState<ConntrackFilter>(initial.filter);

  useEffect(() => {
    setSearchParams(encodeConntrackExplorerState(filter), { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately excludes setSearchParams, which never changes after mount
  }, [filter]);

  function patchFilter(patch: Partial<ConntrackFilter>): void {
    setFilter((prev) => ({ ...prev, ...patch }));
  }

  const { data, isLoading, error, refetch } = useConntrackQuery(filter);
  const items = data?.items ?? [];

  return (
    <div className="flex flex-col gap-3">
      <PageHeader
        title="Conntrack explorer"
        description="Live, per-node conntrack/NAT table, cluster-wide. Read-only — no flush/delete of any connection."
      />

      <FilterBar filter={filter} onChange={patchFilter} />

      {data?.partial && data.failedNodes && data.failedNodes.length > 0 && (
        <p role="status" className="rounded-md bg-amber-50 px-3 py-1.5 text-sm text-amber-800 dark:bg-amber-950/40 dark:text-amber-300">
          Could not reach: {data.failedNodes.join(", ")}. Results from those nodes are missing this refresh.
        </p>
      )}

      {isLoading && <p className="text-sm text-fg-muted">Loading…</p>}
      {error && (
        <EmptyState
          icon="static-route"
          variant="failed"
          title="Could not load conntrack data"
          description="Try again in a moment."
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !error && items.length === 0 && (
        <EmptyState
          icon="static-route"
          variant="filtered"
          title="No live connections match the current filter"
          description="Try widening or clearing a filter."
          action={
            <Button variant="secondary" size="sm" onClick={() => { setFilter({}); }}>
              Clear filters
            </Button>
          }
        />
      )}

      {!isLoading && !error && items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Proto</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Destination</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Timeout</TableHead>
              <TableHead>NAT source</TableHead>
              <TableHead>NAT destination</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((e, i) => (
              <ConntrackRow key={`${e.node}-${String(i)}`} entry={e} />
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

function ConntrackRow({ entry }: { entry: ConntrackEntry }) {
  return (
    <TableRow>
      <TableCell>{entry.node}</TableCell>
      <TableCell className="font-mono text-xs">{protoName(entry.proto)}</TableCell>
      <TableCell className="font-mono text-xs">{endpoint(entry.srcIp, entry.srcPort)}</TableCell>
      <TableCell className="font-mono text-xs">{endpoint(entry.dstIp, entry.dstPort)}</TableCell>
      <TableCell>{entry.state ?? "—"}</TableCell>
      <TableCell>{formatTimeout(entry.timeoutSec)}</TableCell>
      <TableCell className="font-mono text-xs">{natLabel(entry.natSrc)}</TableCell>
      <TableCell className="font-mono text-xs">{natLabel(entry.natDst)}</TableCell>
    </TableRow>
  );
}
