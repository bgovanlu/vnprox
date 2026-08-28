// SPDX-License-Identifier: Apache-2.0

// Tools → Multicast/MDB browser (docs/api.md's Multicast/MDB section,
// T-3902): a deliberate sibling of MacFdbBrowser.tsx, matching its
// conventions (query-box search, cluster-wide node-tagged rows, the same
// EmptyState idiom) but backed by GET /mdb's live cluster fan-out rather
// than an inventory-backed listing — see mdb.ts's doc comment for why
// (there is no netlink MDB dump feeding inventory the way FDB has). A
// second summary table (per-bridge IGMP/MLD-snooping config) sits below
// the entries table — the task card's "per-bridge snooping enabled/
// disabled state is shown" requirement, which the MAC/FDB browser has no
// analog of.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchMDB } from "../api/mdb";
import type { MDBBridge, MDBEntry } from "../api/types";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";

function RouterModeLabel({ mode }: { mode: number }) {
  // Kernel's raw multicast_router sysfs value (docs/api.md's MDBBridge
  // doc): 0 (never), 1 (learn/auto — the only value observed on a real PVE
  // 9.2.4 host), 2 (permanent). Values outside 0-2 are shown as-is rather
  // than guessed into one of the three, since nothing this codebase has
  // observed rules that out.
  switch (mode) {
    case 0:
      return <span>Disabled</span>;
    case 1:
      return <span>Auto (learn)</span>;
    case 2:
      return <span>Permanent</span>;
    default:
      return <span>{mode}</span>;
  }
}

function SnoopingBridgesTable({ bridges }: { bridges: MDBBridge[] }) {
  if (bridges.length === 0) {
    return (
      <EmptyState
        icon="bridge"
        variant="empty"
        title="No bridge snooping state reported"
        description="No reachable node returned any bridge multicast configuration."
        density="compact"
      />
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Node</TableHead>
          <TableHead>Bridge</TableHead>
          <TableHead>Snooping</TableHead>
          <TableHead>Querier</TableHead>
          <TableHead>Router mode</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {bridges.map((b) => (
          <TableRow key={`${b.node}/${b.bridge}`}>
            <TableCell>{b.node}</TableCell>
            <TableCell>{b.bridge}</TableCell>
            <TableCell>
              {b.snooping ? (
                <span className="text-emerald-700 dark:text-emerald-300">enabled</span>
              ) : (
                <span className="text-slate-600 dark:text-slate-400">disabled</span>
              )}
            </TableCell>
            <TableCell>
              {b.querier ? (
                <span className="text-emerald-700 dark:text-emerald-300">yes</span>
              ) : (
                <span className="text-slate-600 dark:text-slate-400">no</span>
              )}
            </TableCell>
            <TableCell>
              <RouterModeLabel mode={b.routerMode} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function EntryRow({ entry }: { entry: MDBEntry }) {
  return (
    <TableRow key={`${entry.node}/${entry.bridge}/${entry.group}/${entry.port ?? ""}`}>
      <TableCell>{entry.node}</TableCell>
      <TableCell>{entry.bridge}</TableCell>
      <TableCell className="font-mono">{entry.group}</TableCell>
      <TableCell>{entry.port ?? "—"}</TableCell>
      <TableCell>{entry.vlan ?? "—"}</TableCell>
      <TableCell>{entry.state ?? "—"}</TableCell>
    </TableRow>
  );
}

export function MulticastMdbBrowser() {
  const [query, setQuery] = useState("");
  const trimmed = query.trim();
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["mdb", trimmed],
    queryFn: () => fetchMDB({ group: trimmed }),
    staleTime: 10_000,
  });
  const entries = data?.entries ?? [];
  const bridges = data?.bridges ?? [];

  return (
    <div className="flex flex-col gap-3">
      <div>
        <h2 className="text-base font-semibold">Multicast / MDB browser</h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Bridge multicast forwarding-database (MDB) entries and IGMP/MLD-snooping configuration, across every
          reachable node — cluster-wide, live. Leave the group box blank to browse everything.
        </p>
      </div>
      <input
        type="text"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
        }}
        placeholder="224.0.0.251, or ff02::fb"
        aria-label="Search multicast group"
        className="w-full max-w-md rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700 dark:bg-slate-900"
      />

      {data?.partial && data.failedNodes && data.failedNodes.length > 0 && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          Could not reach: {data.failedNodes.join(", ")} — showing every other node's data.
        </p>
      )}

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {isError && (
        <EmptyState
          icon="bridge"
          variant="failed"
          title="Could not load the MDB table"
          description="Try again in a moment."
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}
      {!isLoading && !isError && entries.length === 0 && (
        <EmptyState
          icon="bridge"
          variant={trimmed ? "filtered" : "empty"}
          title={trimmed ? "No matches" : "No MDB entries"}
          description={
            trimmed
              ? `Nothing matched "${trimmed}" on any reachable node's bridges.`
              : "Bridges report multicast group membership once IGMP/MLD-snooping has learned some — an empty table is the common state on a host with no active multicast traffic."
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
      {entries.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Bridge</TableHead>
              <TableHead>Group</TableHead>
              <TableHead>Port</TableHead>
              <TableHead>VLAN</TableHead>
              <TableHead>State</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((e) => (
              <EntryRow key={`${e.node}/${e.bridge}/${e.group}/${e.port ?? ""}`} entry={e} />
            ))}
          </TableBody>
        </Table>
      )}

      <div>
        <h3 className="text-sm font-semibold text-slate-800 dark:text-slate-200">Bridge snooping state</h3>
        {!isLoading && !isError && <SnoopingBridgesTable bridges={bridges} />}
      </div>
    </div>
  );
}
