// T-1003's Flow Explorer: a filterable/sortable/aggregatable table over
// GET /flows (docs/api.md's Flows section, T-1002) — read-only, no
// mutation. Filter state, sort, and aggregation mode all round-trip
// through the URL (urlState.ts), the same convention the path simulator
// (simulator/urlState.ts) and saved topology views (topology/savedViews.ts)
// already established, so a link into this page (including the map's
// guest-pair drill-down — topology/FlowPairPanel.tsx) reproduces the exact
// same filtered/sorted/aggregated view.
import { useEffect, useMemo, useReducer } from "react";
import { useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { FlowRecord } from "../api/types";
import { protoName } from "./proto";
import { useFlowsQuery, useFlowsWsBridge } from "./flowsQueries";
import { useK8sClustersQuery, useK8sOverlaysQuery } from "../topology/layers/k8sQueries";
import {
  attributeK8sFlow,
  buildK8sAttributionIndex,
  formatK8sAttribution,
  type K8sAttributionIndex,
} from "../topology/layers/k8sFlowAttribution";
import {
  aggregateConversations,
  flowReducer,
  initialFlowViewState,
  selectVisibleFlows,
  sortConversations,
  sortFlows,
  RENDER_CAP,
  type FlowFilterState,
  type FlowSortKey,
  type FlowViewMode,
} from "./reducer";
import { decodeFlowExplorerState, encodeFlowExplorerState } from "./urlState";

const SFLOW_ORG_URL = "https://sflow.org";

function formatBytes(n: number): string {
  if (n < 1024) return `${String(n)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex] ?? "TB"}`;
}

function formatTime(at: number): string {
  return new Date(at * 1000).toLocaleTimeString();
}

/** T-1504: a small text badge for a flow row's serviceClass — "—" for an
 * absent field (no FlowClassifier wired daemon-side) so this reads as
 * "unknown", distinct from the classifier's own explicit "unclassified"
 * verdict (a registered NetworkSource ran and found no match). */
function serviceClassLabel(serviceClass: FlowRecord["serviceClass"]): string {
  return serviceClass ?? "—";
}

/** Renders a resolved ref if present, otherwise the raw IP — every record's
 * endpoints are shown honestly (never a guessed ref), mirroring
 * FlowRecord's own "srcRef/dstRef populated only when resolved" contract. */
function endpointLabel(ip: string, ref: string | undefined): string {
  return ref ? `${ref} (${ip})` : ip;
}

/** T-1502: the `k8sService` column's label — display-only, never a filter
 * that hides an unresolved row (this task's card, verbatim), so an address
 * outside every registered cluster's pod/service CIDR simply shows "—",
 * the same "absent, not a wrong guess" convention serviceClassLabel above
 * already follows for T-1504's field. Computed client-side
 * (k8sFlowAttribution.ts) against every registered k8s cluster's overlay —
 * see this file's own useK8sAttributionIndex for why. */
function k8sServiceLabel(index: K8sAttributionIndex, record: Pick<FlowRecord, "srcIp" | "dstIp">): string {
  const ref = attributeK8sFlow(index, record);
  return ref ? formatK8sAttribution(ref) : "—";
}

/** Indexes every currently-registered k8s cluster's live overlay for the
 * `k8sService` column — independent of the map's own "Kubernetes" layer
 * toggle (topology/TopologyPage.tsx's k8sLayerActive): this is a read-only
 * display column on a different page, so it fetches whenever at least one
 * cluster is registered, the same "always populate a display-only
 * attribution column" precedent serviceClass's own wiring sets. */
function useK8sAttributionIndex(): K8sAttributionIndex {
  const { data: clusters } = useK8sClustersQuery(true);
  const { overlays } = useK8sOverlaysQuery(clusters, true);
  return useMemo(() => buildK8sAttributionIndex(overlays), [overlays]);
}

function FilterBar({ filter, onChange }: { filter: FlowFilterState; onChange: (patch: Partial<FlowFilterState>) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input
        aria-label="Filter by guest/bridge/vnet ref"
        placeholder="guest ref"
        value={filter.guest}
        onChange={(e) => { onChange({ guest: e.target.value }); }}
        className="w-48 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <input
        aria-label="Filter by VLAN"
        placeholder="vlan"
        value={filter.vlan}
        onChange={(e) => { onChange({ vlan: e.target.value }); }}
        className="w-20 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <input
        aria-label="Filter by subnet (CIDR)"
        placeholder="subnet (CIDR)"
        value={filter.subnet}
        onChange={(e) => { onChange({ subnet: e.target.value }); }}
        className="w-36 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <input
        aria-label="Filter by port"
        placeholder="port"
        value={filter.port}
        onChange={(e) => { onChange({ port: e.target.value }); }}
        className="w-20 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <input
        aria-label="Filter by protocol"
        placeholder="protocol (e.g. tcp)"
        value={filter.protocol}
        onChange={(e) => { onChange({ protocol: e.target.value }); }}
        className="w-36 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </div>
  );
}

export function FlowExplorer() {
  const [searchParams, setSearchParams] = useSearchParams();
  const urlState = useMemo(() => decodeFlowExplorerState(searchParams), []); // eslint-disable-line react-hooks/exhaustive-deps

  const [state, dispatch] = useReducer(flowReducer, {
    ...initialFlowViewState,
    filter: urlState.filter,
    sort: urlState.sort,
    view: urlState.view,
  });

  // Keep the URL in sync with filter/sort/view so "Copy link"/browser
  // back-forward/a bookmark all reproduce the current view exactly.
  useEffect(() => {
    const next = encodeFlowExplorerState({
      filter: state.filter,
      sort: state.sort,
      view: state.view,
      pairSrc: urlState.pairSrc,
      pairDst: urlState.pairDst,
    });
    setSearchParams(next, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately excludes setSearchParams/urlState.pair*, which never change after mount
  }, [state.filter, state.sort, state.view]);

  // GET /flows' own server-side filter only ever narrows by ONE ref
  // (guest matches either srcRef or dstRef) — a map drill-down for a
  // specific *pair* (pairSrc/pairDst) additionally narrows client-side
  // below (see the doc comment on urlState.ts's FlowExplorerUrlState).
  const apiFilter = useMemo(
    () => ({
      guest: state.filter.guest || undefined,
      vlan: state.filter.vlan && Number.isFinite(Number(state.filter.vlan)) ? Number(state.filter.vlan) : undefined,
      subnet: state.filter.subnet || undefined,
      port: state.filter.port && Number.isFinite(Number(state.filter.port)) ? Number(state.filter.port) : undefined,
      protocol: state.filter.protocol || undefined,
    }),
    [state.filter.guest, state.filter.vlan, state.filter.subnet, state.filter.port, state.filter.protocol],
  );

  const { data, isLoading, error } = useFlowsQuery(apiFilter);
  const k8sIndex = useK8sAttributionIndex();

  useEffect(() => {
    if (data) {
      dispatch({ type: "loaded", items: data.items });
    }
  }, [data]);

  useFlowsWsBridge((evt) => {
    dispatch({ type: "batch", entries: evt.entries, droppedTotal: evt.droppedTotal });
  });

  const visible = selectVisibleFlows(state);
  const pairFiltered = useMemo(() => {
    if (!urlState.pairSrc || !urlState.pairDst) return visible;
    return visible.filter(
      (r) =>
        (r.srcRef === urlState.pairSrc || r.srcIp === urlState.pairSrc) &&
        (r.dstRef === urlState.pairDst || r.dstIp === urlState.pairDst),
    );
  }, [visible, urlState.pairSrc, urlState.pairDst]);

  const sortedRaw = useMemo(() => sortFlows(pairFiltered, state.sort), [pairFiltered, state.sort]);
  const conversationRows = useMemo(
    () => sortConversations(aggregateConversations(pairFiltered), state.sort),
    [pairFiltered, state.sort],
  );

  const renderedRaw = sortedRaw.slice(0, RENDER_CAP);
  const renderedConversations = conversationRows.slice(0, RENDER_CAP);
  const isConversationView = state.view === "conversations";
  const totalDropped = state.clientDroppedTotal + state.serverDroppedTotal;

  // Empty cluster-wide (AC4): no items from the initial fetch AND nothing
  // has arrived live this session either — the exact "no flow source
  // configured" signal this task's card describes (T-1002's listeners all
  // off / T-1004 not enabled), inferred purely from the data rather than
  // inspecting config flags this page has no route to read.
  const noFlowsAtAll = !isLoading && !error && state.records.length === 0;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-base font-semibold">Flow explorer</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            Cluster-wide ingested flow records (sFlow/NetFlow/IPFIX), live-following via WebSocket. Read-only — no
            mutation.
          </p>
        </div>
        <div role="tablist" aria-label="Flow explorer view" className="flex items-center gap-1">
          {(["raw", "conversations"] as FlowViewMode[]).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={state.view === v}
              onClick={() => { dispatch({ type: "setView", view: v }); }}
              className={
                state.view === v
                  ? "rounded-md bg-accent-600/10 px-3 py-1.5 text-sm font-medium text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
                  : "rounded-md px-3 py-1.5 text-sm font-medium text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800/60"
              }
            >
              {v === "raw" ? "Flows" : "Conversations"}
            </button>
          ))}
        </div>
      </div>

      {urlState.pairSrc && urlState.pairDst && (
        <p className="rounded-md bg-sky-50 px-3 py-1.5 text-sm text-sky-800 dark:bg-sky-950/40 dark:text-sky-300">
          Showing only the conversation between <span className="font-mono">{urlState.pairSrc}</span> and{" "}
          <span className="font-mono">{urlState.pairDst}</span> (from the map).{" "}
          <button
            type="button"
            className="underline"
            onClick={() => {
              const next = new URLSearchParams(searchParams);
              next.delete("pairSrc");
              next.delete("pairDst");
              setSearchParams(next, { replace: true });
            }}
          >
            Clear
          </button>
        </p>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <FilterBar filter={state.filter} onChange={(patch) => { dispatch({ type: "setFilter", filter: patch }); }} />
        <div className="flex items-center gap-2">
          <label className="text-sm text-slate-500 dark:text-slate-400" htmlFor="flow-sort">
            Sort
          </label>
          <select
            id="flow-sort"
            aria-label="Sort by"
            value={state.sort}
            onChange={(e) => { dispatch({ type: "setSort", sort: e.target.value as FlowSortKey }); }}
            className="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="recency">Most recent</option>
            <option value="bytes">Bytes</option>
            <option value="packets">Packets</option>
          </select>
          <button
            type="button"
            onClick={() => { dispatch({ type: "clear" }); }}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium dark:border-slate-700"
          >
            Clear buffer
          </button>
        </div>
      </div>

      {totalDropped > 0 && (
        <p role="status" className="rounded-md bg-amber-50 px-3 py-1.5 text-sm text-amber-800 dark:bg-amber-950/40 dark:text-amber-300">
          {totalDropped.toLocaleString()} records dropped to keep up with volume (rate cap engaged).
        </p>
      )}

      {isLoading && <p className="text-sm text-slate-400">Loading…</p>}
      {error && <EmptyState title="Could not load flow records" description="Try again in a moment." />}

      {noFlowsAtAll && (
        <EmptyState
          title="No flow records yet"
          description="vnprox has no ingested flow records cluster-wide. Configure an sFlow/NetFlow/IPFIX exporter (or host-local sampling) pointed at a node, then enable it in that node's vnprox.toml [flows] section."
          action={
            <a href={SFLOW_ORG_URL} target="_blank" rel="noreferrer" className="text-sm underline">
              Flow source setup reference
            </a>
          }
        />
      )}

      {!isLoading && !error && !noFlowsAtAll && !isConversationView && renderedRaw.length === 0 && (
        <EmptyState title="No flows match the current filter" description="Try widening or clearing a filter." />
      )}
      {!isLoading && !error && !noFlowsAtAll && isConversationView && renderedConversations.length === 0 && (
        <EmptyState title="No conversations match the current filter" description="Try widening or clearing a filter." />
      )}

      {!isConversationView && renderedRaw.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Node</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Destination</TableHead>
              <TableHead>Proto</TableHead>
              <TableHead>Bytes</TableHead>
              <TableHead>Packets</TableHead>
              <TableHead>VLAN</TableHead>
              <TableHead>Service</TableHead>
              <TableHead>k8s service</TableHead>
              <TableHead>Origin</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {renderedRaw.map((r, i) => (
              <FlowRow key={`${String(r.at)}-${String(i)}`} record={r} k8sIndex={k8sIndex} />
            ))}
          </TableBody>
        </Table>
      )}

      {isConversationView && renderedConversations.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Source</TableHead>
              <TableHead>Destination</TableHead>
              <TableHead>Bytes</TableHead>
              <TableHead>Packets</TableHead>
              <TableHead>Records</TableHead>
              <TableHead>Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {renderedConversations.map((row) => (
              <TableRow key={row.key}>
                <TableCell className="font-mono text-xs">{endpointLabel(row.srcIp, row.srcRef)}</TableCell>
                <TableCell className="font-mono text-xs">{endpointLabel(row.dstIp, row.dstRef)}</TableCell>
                <TableCell>{formatBytes(row.bytes)}</TableCell>
                <TableCell>{row.packets.toLocaleString()}</TableCell>
                <TableCell>{row.recordCount.toLocaleString()}</TableCell>
                <TableCell className="whitespace-nowrap font-mono text-xs">{formatTime(row.lastAt)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

function FlowRow({ record, k8sIndex }: { record: FlowRecord; k8sIndex: K8sAttributionIndex }) {
  return (
    <TableRow>
      <TableCell className="whitespace-nowrap font-mono text-xs">{formatTime(record.at)}</TableCell>
      <TableCell>{record.node}</TableCell>
      <TableCell className="font-mono text-xs">
        {endpointLabel(record.srcIp, record.srcRef)}
        {record.srcPort !== undefined ? `:${String(record.srcPort)}` : ""}
      </TableCell>
      <TableCell className="font-mono text-xs">
        {endpointLabel(record.dstIp, record.dstRef)}
        {record.dstPort !== undefined ? `:${String(record.dstPort)}` : ""}
      </TableCell>
      <TableCell className="font-mono text-xs">{protoName(record.proto)}</TableCell>
      <TableCell>{formatBytes(record.bytes)}</TableCell>
      <TableCell>{record.packets.toLocaleString()}</TableCell>
      <TableCell>{record.vlan ?? "—"}</TableCell>
      <TableCell className="text-xs">{serviceClassLabel(record.serviceClass)}</TableCell>
      <TableCell className="text-xs">{k8sServiceLabel(k8sIndex, record)}</TableCell>
      <TableCell className="text-xs text-slate-500 dark:text-slate-400">{record.source}</TableCell>
    </TableRow>
  );
}
