// SPDX-License-Identifier: Apache-2.0

// Tools → MAC/FDB browser (docs/features/lldp-discovery.md §4): query any
// MAC/partial → per-node bridge/port hits + owning guest deep-link. GET
// /fdb does the actual list-vs-search branching (blank ?mac= lists
// everything cluster-wide; non-blank ranks partial matches) — this
// component just reflects whatever the query box currently holds.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import clsx from "clsx";
import { fetchFDB } from "../api/fdb";
import type { FDBOwner, FDBRow } from "../api/types";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useTopologyStore } from "../topology/store";

const OWNER_LABEL: Record<FDBOwner, string> = {
  guest: "Guest",
  "vnprox-known": "vnprox-known",
  unknown: "Unknown",
};

const OWNER_BADGE_CLASS: Record<FDBOwner, string> = {
  guest: "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300",
  "vnprox-known": "bg-slate-100 text-fg-muted dark:bg-slate-800",
  unknown: "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300",
};

function OwnerBadge({ row }: { row: FDBRow }) {
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);
  const badge = (
    <span className={clsx("rounded px-1.5 py-0.5 text-xs font-medium", OWNER_BADGE_CLASS[row.owner])}>
      {OWNER_LABEL[row.owner]}
    </span>
  );
  if (!row.ownerRef) {
    return badge;
  }
  return (
    <button
      type="button"
      title={`Open ${row.ownerLabel ?? row.ownerRef} in the topology inspector`}
      onClick={() => {
        select(row.ownerRef);
        void navigate("/topology");
      }}
      className="flex items-center gap-1.5 rounded hover:underline"
    >
      {badge}
      {row.ownerLabel && <span className="text-xs text-fg-muted">{row.ownerLabel}</span>}
    </button>
  );
}

export function MacFdbBrowser() {
  const [query, setQuery] = useState("");
  const trimmed = query.trim();
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["fdb", trimmed],
    queryFn: () => fetchFDB(trimmed),
    staleTime: 10_000,
  });
  const rows = data ?? [];

  return (
    <div className="flex flex-col gap-3">
      <div>
        <h2 className="text-base font-semibold">MAC / FDB browser</h2>
        <p className="text-sm text-fg-muted">
          Search any MAC (full or partial) across every reachable node's bridge forwarding tables — cluster-wide,
          ranked by match quality. Leave blank to browse every learned entry.
        </p>
      </div>
      <input
        type="text"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
        }}
        placeholder="aa:bb:cc:dd:ee:ff, or just aabb"
        aria-label="Search MAC address"
        className="w-full max-w-md rounded-md border border-border-strong bg-white px-2.5 py-1.5 text-sm outline-none focus:border-accent-500 dark:bg-slate-900"
      />

      {isLoading && <p className="text-sm text-fg-muted">Loading…</p>}
      {isError && (
        <EmptyState
          icon="bridge"
          variant="failed"
          title="Could not load the FDB"
          description="Try again in a moment."
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}
      {!isLoading && !isError && rows.length === 0 && (
        <EmptyState
          icon="bridge"
          variant={trimmed ? "filtered" : "empty"}
          title={trimmed ? "No matches" : "No FDB entries yet"}
          description={
            trimmed
              ? `Nothing matched "${trimmed}" on any reachable node's bridges.`
              : "Bridges report their forwarding table once traffic has been learned on them."
          }
          action={
            trimmed ? (
              <Button variant="secondary" size="sm" onClick={() => { setQuery(""); }}>
                Clear filters
              </Button>
            ) : undefined
          }
        />
      )}
      {rows.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Bridge</TableHead>
              <TableHead>Port</TableHead>
              <TableHead>VLAN</TableHead>
              <TableHead>MAC</TableHead>
              <TableHead>Owner</TableHead>
              <TableHead>Stale</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={`${r.node}/${r.bridgeRef}/${r.mac}/${r.port ?? ""}`}>
                <TableCell>{r.node}</TableCell>
                <TableCell>{r.bridge}</TableCell>
                <TableCell>{r.port ?? "—"}</TableCell>
                <TableCell>{r.vlan ?? "—"}</TableCell>
                <TableCell className="font-mono">{r.mac}</TableCell>
                <TableCell>
                  <OwnerBadge row={r} />
                </TableCell>
                <TableCell>
                  {r.stale ? (
                    <span className="text-amber-600 dark:text-amber-400">stale</span>
                  ) : (
                    <span className="text-fg-muted">fresh</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
