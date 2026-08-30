// SPDX-License-Identifier: Apache-2.0

// T-3903's route explorer: kernel FIB + policy rules + FRR RIB per node, a
// visual next-hop graph, and the "which path would this address take"
// lookup. Read-only throughout — no route/rule add-delete affordance
// anywhere on this page.
//
// This is NOT a competitor to the path simulator (web/src/simulator/) —
// internal/sim's l3Path explicitly declines to evaluate routing once a
// flow leaves vnprox's own SDN inventory model (see internal/sim/l3.go's
// FeatureExternalRouting caveat and internal/route's package doc comment
// for the full cross-reference). This page answers the question that
// caveat discloses it cannot: what does this node's *actual* kernel/FRR
// routing state do with a destination, on-fabric or not.
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { FIBRoute } from "../api/types";
import { NextHopGraph } from "./NextHopGraph";
import { useRouteLookupQuery, useRouteNodesQuery, useRouteSnapshotQuery } from "./routeQueries";

const inputClass =
  "rounded-md border border-border-strong bg-white px-2 py-1 text-sm dark:bg-slate-900";

function fibRouteDisplayKey(r: FIBRoute): string {
  return `${r.afi}|${r.table}|${r.type}|${r.dst}|${r.dev}|${r.gateway ?? ""}`;
}

export function RouteExplorerPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const nodesQuery = useRouteNodesQuery();
  const nodesData = nodesQuery.data;
  const nodes = nodesData?.nodes ?? [];

  const [node, setNode] = useState(() => searchParams.get("node") ?? "");
  const [dst, setDst] = useState(() => searchParams.get("dst") ?? "");
  const [iface, setIface] = useState(() => searchParams.get("iface") ?? "");
  const [submittedDst, setSubmittedDst] = useState(() => searchParams.get("dst") ?? "");

  // Once the node list resolves, default to the first node if none is
  // selected yet (covers both "fresh visit" and "the requested ?node=
  // isn't in the list") — mirrors how a node-scoped page elsewhere in this
  // app (e.g. SDN's DhcpView) seeds its own default from the first
  // available option. Depends on `nodesData` (the query's own stable
  // object identity, unchanged across re-renders until the data itself
  // changes) rather than the `nodes` array derived from it above, which is
  // a fresh `[]`/mapped array literal on every render and would otherwise
  // re-run this effect every render.
  useEffect(() => {
    const first = nodesData?.nodes[0];
    if (node === "" && first !== undefined) {
      setNode(first);
    }
  }, [node, nodesData]);

  useEffect(() => {
    const params: Record<string, string> = {};
    if (node) params.node = node;
    if (submittedDst) params.dst = submittedDst;
    if (iface) params.iface = iface;
    setSearchParams(params, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately excludes setSearchParams, which never changes after mount
  }, [node, submittedDst, iface]);

  const snapshotQuery = useRouteSnapshotQuery(node, node !== "");
  const lookupQuery = useRouteLookupQuery(node, submittedDst, iface, submittedDst !== "");

  const snapshot = snapshotQuery.data;
  const lookup = lookupQuery.data;

  const highlightedRoute = useMemo(() => lookup?.matchedRoute, [lookup]);

  function handleLookupSubmit(e: React.FormEvent): void {
    e.preventDefault();
    setSubmittedDst(dst.trim());
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Route explorer"
        description="Kernel FIB, policy rules, and FRR RIB per node — plus which path a destination would actually take. Read-only."
        actions={
          <label className="flex items-center gap-2 text-sm">
            Node
            <select
              aria-label="Node"
              value={node}
              onChange={(e) => {
                setNode(e.target.value);
              }}
              className={inputClass}
            >
              {nodes.length === 0 && <option value="">(no nodes)</option>}
              {nodes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        }
      />

      <form onSubmit={handleLookupSubmit} aria-label="Which path would this address take" className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1 text-xs">
          Destination address
          <input
            aria-label="Destination address"
            placeholder="e.g. 8.8.8.8 or fe80::1"
            value={dst}
            onChange={(e) => {
              setDst(e.target.value);
            }}
            className={inputClass}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs">
          Interface hint (optional)
          <input
            aria-label="Interface hint"
            placeholder="e.g. vmbr0"
            value={iface}
            onChange={(e) => {
              setIface(e.target.value);
            }}
            className={inputClass}
          />
        </label>
        <button
          type="submit"
          disabled={dst.trim() === ""}
          className="rounded-full bg-blue-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Which path?
        </button>
      </form>

      {lookupQuery.isFetching && <p className="text-sm text-fg-muted">Looking up…</p>}
      {lookupQuery.error && (
        <p role="alert" className="rounded-md bg-red-50 px-3 py-1.5 text-sm text-red-800 dark:bg-red-950/40 dark:text-red-300">
          {lookupQuery.error instanceof Error ? lookupQuery.error.message : "Lookup failed."}
        </p>
      )}
      {lookup && !lookupQuery.isFetching && (
        <div
          role="status"
          className="rounded-md border border-border bg-white p-3 text-sm dark:bg-slate-900"
        >
          {lookup.reachable && lookup.matchedRoute ? (
            <p>
              <strong>{lookup.dst}</strong> would go via{" "}
              <span className="font-mono">{lookup.matchedRoute.dev}</span>
              {lookup.matchedRoute.gateway ? (
                <>
                  {" "}
                  (gateway <span className="font-mono">{lookup.matchedRoute.gateway}</span>)
                </>
              ) : (
                " (on-link)"
              )}
              , matched route <span className="font-mono">{lookup.matchedRoute.dst}</span> in table{" "}
              <span className="font-mono">{lookup.matchedRoute.table}</span>
              {lookup.matchedRule ? (
                <>
                  {" "}
                  via rule priority {lookup.matchedRule.priority}.
                </>
              ) : (
                "."
              )}
            </p>
          ) : lookup.ambiguous && lookup.ambiguous.length > 0 ? (
            <p>
              <strong>{lookup.dst}</strong> is reachable via more than one equally-specific interface
              (<span className="font-mono">{lookup.ambiguous.join(", ")}</span>) — enter an interface hint above to
              disambiguate, the same way <code>ip route get</code> itself requires a <code>dev</code> for this
              destination.
            </p>
          ) : (
            <p>
              <strong>{lookup.dst}</strong> is not reachable from any evaluated routing table.
            </p>
          )}
          {lookup.rulesSkipped && lookup.rulesSkipped.length > 0 && (
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              Not evaluated: {lookup.rulesSkipped.join("; ")}
            </p>
          )}
          {lookup.trace && lookup.trace.length > 0 && (
            <details className="mt-2">
              <summary className="cursor-pointer text-xs text-fg-muted">Why</summary>
              <ol className="mt-1 list-decimal pl-5 text-xs text-fg-muted">
                {lookup.trace.map((step, i) => (
                  <li key={i}>{step}</li>
                ))}
              </ol>
            </details>
          )}
        </div>
      )}

      {snapshotQuery.isLoading && <p className="text-sm text-fg-muted">Loading routing state…</p>}
      {snapshotQuery.error && (
        <EmptyState
          icon="static-route"
          variant="failed"
          title="Could not load routing state"
          description="Try again in a moment, or pick a different node."
          action={
            <Button variant="secondary" size="sm" onClick={() => void snapshotQuery.refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {snapshot && (
        <>
          <section aria-labelledby="next-hop-graph-heading" className="flex flex-col gap-2">
            <h2 id="next-hop-graph-heading" className="text-sm font-semibold text-fg">
              Next-hop graph
            </h2>
            <NextHopGraph routes={snapshot.fib} highlighted={highlightedRoute} />
          </section>

          <section aria-labelledby="fib-heading" className="flex flex-col gap-2">
            <h2 id="fib-heading" className="text-sm font-semibold text-fg">
              Kernel FIB
            </h2>
            {snapshot.fib.length === 0 ? (
              <EmptyState icon="static-route" variant="empty" title="No routes" density="compact" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>AFI</TableHead>
                    <TableHead>Table</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Destination</TableHead>
                    <TableHead>Gateway</TableHead>
                    <TableHead>Device</TableHead>
                    <TableHead>Metric</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {snapshot.fib.map((r) => {
                    const isHighlighted =
                      highlightedRoute !== undefined && fibRouteDisplayKey(r) === fibRouteDisplayKey(highlightedRoute);
                    return (
                      <TableRow key={fibRouteDisplayKey(r)} className={isHighlighted ? "bg-blue-50 dark:bg-blue-950/30" : undefined}>
                        <TableCell>{r.afi}</TableCell>
                        <TableCell>{r.table}</TableCell>
                        <TableCell>{r.type}</TableCell>
                        <TableCell className="font-mono text-xs">{r.dst}</TableCell>
                        <TableCell className="font-mono text-xs">{r.gateway ?? "—"}</TableCell>
                        <TableCell className="font-mono text-xs">{r.dev}</TableCell>
                        <TableCell>{r.metric ?? "—"}</TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </section>

          <section aria-labelledby="rules-heading" className="flex flex-col gap-2">
            <h2 id="rules-heading" className="text-sm font-semibold text-fg">
              Policy rules
            </h2>
            {snapshot.rules.length === 0 ? (
              <EmptyState icon="static-route" variant="empty" title="No policy rules" density="compact" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Priority</TableHead>
                    <TableHead>AFI</TableHead>
                    <TableHead>From</TableHead>
                    <TableHead>Table</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {snapshot.rules.map((r) => (
                    <TableRow key={`${r.afi}-${String(r.priority)}-${r.table}`}>
                      <TableCell>{r.priority}</TableCell>
                      <TableCell>{r.afi}</TableCell>
                      <TableCell className="font-mono text-xs">{r.src}</TableCell>
                      <TableCell className="font-mono text-xs">{r.table}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </section>

          <section aria-labelledby="rib-heading" className="flex flex-col gap-2">
            <h2 id="rib-heading" className="text-sm font-semibold text-fg">
              FRR RIB
            </h2>
            {snapshot.frrUnavailable ? (
              <EmptyState
                icon="static-route"
                variant="unconfigured"
                title="FRR is not running on this node"
                description="No SDN EVPN zone configured — this is the common case."
                density="compact"
                action={
                  <Button variant="secondary" size="sm" onClick={() => { void navigate("/sdn"); }}>
                    Go to SDN
                  </Button>
                }
              />
            ) : !snapshot.rib || snapshot.rib.length === 0 ? (
              <EmptyState icon="static-route" variant="empty" title="No RIB entries" density="compact" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>AFI</TableHead>
                    <TableHead>VRF</TableHead>
                    <TableHead>Prefix</TableHead>
                    <TableHead>Protocol</TableHead>
                    <TableHead>Next hops</TableHead>
                    <TableHead>Selected</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {snapshot.rib.map((r) => (
                    <TableRow key={`${r.afi}-${r.vrf}-${r.prefix}-${r.protocol}`}>
                      <TableCell>{r.afi}</TableCell>
                      <TableCell className="font-mono text-xs">{r.vrf}</TableCell>
                      <TableCell className="font-mono text-xs">{r.prefix}</TableCell>
                      <TableCell>{r.protocol}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {r.nexthops.map((nh) => nh.ip ?? nh.interface).join(", ") || "—"}
                      </TableCell>
                      <TableCell>{r.selected ? "yes" : "no"}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </section>
        </>
      )}
    </div>
  );
}
