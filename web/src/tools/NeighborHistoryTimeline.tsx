// SPDX-License-Identifier: Apache-2.0

// Tools → Neighbor binding timeline (docs/api.md's "Neighbor binding
// history" section, T-3905): "the ARP/IPv6-neighbor table now" turned into
// "what changed and when", grouped per (node, ip) with flap sequences
// visually distinguished from a single clean rebind. Read-only — stages
// and applies nothing, sibling to the MAC/FDB and multicast/MDB browsers
// on this same page.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchNeighborHistory } from "../api/neighborHistory";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { findMacClaims, groupNeighborHistory } from "./neighborHistoryFlap";

function formatAt(at: number): string {
  return new Date(at * 1000).toLocaleString();
}

const DEFAULT_PAGE_LIMIT = 200;

export function NeighborHistoryTimeline() {
  const [ip, setIp] = useState("");
  const [mac, setMac] = useState("");
  const [node, setNode] = useState("");

  const filter = { ip: ip.trim(), mac: mac.trim(), node: node.trim(), limit: DEFAULT_PAGE_LIMIT };
  const hasFilter = filter.ip !== "" || filter.mac !== "" || filter.node !== "";
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["neighbor-history", filter.ip, filter.mac, filter.node],
    queryFn: () =>
      fetchNeighborHistory({
        ip: filter.ip || undefined,
        mac: filter.mac || undefined,
        node: filter.node || undefined,
        limit: filter.limit,
      }),
    staleTime: 10_000,
  });

  const items = data?.items ?? [];
  const groups = groupNeighborHistory(items);
  const macClaims = findMacClaims(items);

  return (
    <div className="flex flex-col gap-3">
      <div>
        <h2 className="text-base font-semibold">Neighbor binding timeline</h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          IP↔MAC binding history across the cluster — every recorded transition, not just the current snapshot.
          Bindings that flap (an IP rapidly changing MAC, or one MAC claiming many IPs) are called out separately
          from a single clean rebind. Bounded to a 24-hour window; see the{" "}
          <code className="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">neighbor_binding_flap</code>{" "}
          finding for the authoritative, always-on flap alarm.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          value={ip}
          onChange={(e) => {
            setIp(e.target.value);
          }}
          placeholder="Filter by IP"
          aria-label="Filter by IP address"
          className="w-44 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700 dark:bg-slate-900"
        />
        <input
          type="text"
          value={mac}
          onChange={(e) => {
            setMac(e.target.value);
          }}
          placeholder="Filter by MAC"
          aria-label="Filter by MAC address"
          className="w-44 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700 dark:bg-slate-900"
        />
        <input
          type="text"
          value={node}
          onChange={(e) => {
            setNode(e.target.value);
          }}
          placeholder="Filter by node"
          aria-label="Filter by node"
          className="w-36 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700 dark:bg-slate-900"
        />
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {isError && (
        <EmptyState
          icon="lldp-neighbor"
          variant="failed"
          title="Could not load binding history"
          description="Try again in a moment."
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}
      {!isLoading && !isError && groups.length === 0 && (
        <EmptyState
          icon="lldp-neighbor"
          variant={hasFilter ? "filtered" : "empty"}
          title="No binding history yet"
          description="A transition is recorded once a node's own resolved neighbor table shows a new or changed IP<->MAC binding."
          action={
            hasFilter ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setIp("");
                  setMac("");
                  setNode("");
                }}
              >
                Clear filters
              </Button>
            ) : undefined
          }
        />
      )}

      {data?.partial && (
        <p className="text-sm text-amber-600 dark:text-amber-400" role="status">
          Partial result — {data.failedNodes?.length ?? 0} node(s) could not be reached: {data.failedNodes?.join(", ")}
        </p>
      )}

      {macClaims.length > 0 && (
        <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-800 dark:bg-amber-950/40">
          <p className="font-medium text-amber-800 dark:text-amber-300">
            {macClaims.length} MAC{macClaims.length > 1 ? "s" : ""} claiming multiple IPs in a short window
          </p>
          <ul className="mt-1 flex flex-col gap-0.5">
            {macClaims.map((c) => (
              <li key={c.key} className="font-mono text-xs text-amber-700 dark:text-amber-300">
                {c.node}: {c.mac} → {c.ips.join(", ")}
              </li>
            ))}
          </ul>
        </div>
      )}

      {groups.length > 0 && (
        <ul className="flex flex-col gap-3" data-testid="binding-groups">
          {groups.map((g) => (
            <li
              key={g.key}
              className={
                g.isFlapping
                  ? "rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/40"
                  : "rounded-md border border-slate-200 p-3 dark:border-slate-800"
              }
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm font-medium">{g.ip}</span>
                <span className="text-xs text-slate-600 dark:text-slate-400">on {g.node}</span>
                {g.isFlapping && (
                  <span
                    className="rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/60 dark:text-amber-300"
                    title="3 or more MAC changes within a 2-minute window — a flapping binding, not a single clean rebind"
                  >
                    Flapping
                  </span>
                )}
                <span className="text-xs text-slate-600 dark:text-slate-400">
                  {g.events.length} event{g.events.length > 1 ? "s" : ""}
                </span>
              </div>
              <ol className="mt-2 flex flex-col gap-1 border-l border-slate-200 pl-3 dark:border-slate-800">
                {g.events.map((e) => (
                  <li key={`${String(e.at)}-${e.mac}`} className="text-sm">
                    <span className="text-xs text-slate-600 dark:text-slate-400">{formatAt(e.at)}</span>{" "}
                    {e.firstSeen ? (
                      <span>
                        first seen as <span className="font-mono">{e.mac}</span>
                      </span>
                    ) : (
                      <span>
                        <span className="font-mono">{e.prevMac}</span> → <span className="font-mono">{e.mac}</span>
                      </span>
                    )}
                    {e.iface && <span className="text-xs text-slate-600 dark:text-slate-400"> on {e.iface}</span>}
                  </li>
                ))}
              </ol>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
