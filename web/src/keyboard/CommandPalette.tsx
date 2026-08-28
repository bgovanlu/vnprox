// SPDX-License-Identifier: Apache-2.0

// T-903: the ⌘K/Ctrl+K command palette. Merges SpotlightSearch's fuzzy
// entity search (GET /inventory/search, docs/features/topology.md §2) with
// every currently-registered palette action (actions.ts) in one dialog —
// "every page action gets a palette verb" per the task card. Mounted once,
// app-wide (AppShell.tsx), like ShortcutHelpDialog and ChangesetDrawer, so
// it's reachable from any page, not just Topology.
//
// T-1202: when a federated deployment has >=2 clusters attached, the palette
// additionally fans the query out across every cluster (GET
// /federation/search), grouping the namespaced hits by cluster and offering
// a "switch to cluster X" action per cluster to change context. With <2
// clusters attached the federation section never appears — the palette is
// byte-identical to its single-cluster self.
import { useEffect, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import clsx from "clsx";
import { useNavigate } from "react-router-dom";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import type { SearchResult } from "../api/types";
import type { FederationSearchHit } from "../api/federation";
import { useSearchQuery } from "../topology/queries";
import {
  federationIsActive,
  useFederationSearchQuery,
  useFederationTopologyQuery,
} from "../topology/federation/federationQueries";
import { useTopologyStore } from "../topology/store";
import { useAllPaletteActions, type PaletteAction } from "./actions";

export interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type PaletteItem =
  | { readonly kind: "entity"; readonly result: SearchResult }
  | { readonly kind: "action"; readonly action: PaletteAction }
  | { readonly kind: "cluster-switch"; readonly clusterId: string; readonly clusterName: string }
  | { readonly kind: "cluster-entity"; readonly hit: FederationSearchHit };

function itemKey(item: PaletteItem): string {
  switch (item.kind) {
    case "entity":
      return `entity:${item.result.ref}`;
    case "action":
      return `action:${item.action.id}`;
    case "cluster-switch":
      return `cluster-switch:${item.clusterId}`;
    case "cluster-entity":
      return `cluster-entity:${item.hit.clusterId}:${item.hit.ref}`;
  }
}

function matchesQuery(action: PaletteAction, query: string): boolean {
  if (!query) return true;
  const needle = query.toLowerCase();
  if (action.label.toLowerCase().includes(needle)) return true;
  if (action.hint?.toLowerCase().includes(needle)) return true;
  return action.keywords?.some((k) => k.toLowerCase().includes(needle)) ?? false;
}

/** Builds the federation half of the list: hits grouped by cluster, each
 * cluster's block led by a "Switch to <cluster>" action. Preserves the
 * backend's already-sorted (clusterName, label) order, so consecutive hits
 * from one cluster stay contiguous under their single switch row. */
function buildFederationItems(hits: readonly FederationSearchHit[]): PaletteItem[] {
  const out: PaletteItem[] = [];
  let currentCluster: string | undefined;
  for (const hit of hits) {
    if (hit.clusterId !== currentCluster) {
      currentCluster = hit.clusterId;
      out.push({ kind: "cluster-switch", clusterId: hit.clusterId, clusterName: hit.clusterName });
    }
    out.push({ kind: "cluster-entity", hit });
  }
  return out;
}

export function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [highlighted, setHighlighted] = useState(0);
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);
  const { data, isFetching } = useSearchQuery(query);
  const entityResults = useMemo(() => data?.results ?? [], [data]);
  const allActions = useAllPaletteActions();
  const actionResults = useMemo(() => allActions.filter((a) => matchesQuery(a, query)), [allActions, query]);

  const { data: fedTopology } = useFederationTopologyQuery(open);
  const fedActive = federationIsActive(fedTopology);
  const { data: fedSearch } = useFederationSearchQuery(query, fedActive);
  const federationItems = useMemo(() => buildFederationItems(fedSearch?.results ?? []), [fedSearch]);

  const items: PaletteItem[] = useMemo(
    () => [
      ...entityResults.map((result): PaletteItem => ({ kind: "entity", result })),
      ...actionResults.map((action): PaletteItem => ({ kind: "action", action })),
      ...federationItems,
    ],
    [entityResults, actionResults, federationItems],
  );

  // Keep the highlighted row in range as the merged list changes size
  // (typing narrows/widens the entity, action, and federation halves at once).
  useEffect(() => {
    setHighlighted(0);
  }, [query, items.length]);

  function close(): void {
    onOpenChange(false);
    setQuery("");
    setHighlighted(0);
  }

  /** Navigates the shared map context to a specific attached cluster
   * (T-1202's "switch to cluster X"): the /topology route's global gate
   * reads `?cluster=<id>` and drills into that cluster's topology. */
  function switchToCluster(clusterId: string): void {
    void navigate(`/topology?cluster=${encodeURIComponent(clusterId)}`);
  }

  function activate(item: PaletteItem): void {
    switch (item.kind) {
      case "entity":
        // Mirrors TopBar.tsx's global search box: the dialog is mounted
        // app-wide, not scoped to the Topology page, so selecting an entity
        // sets the shared selection before navigating there — the same
        // mechanism SpotlightSearch's onSelect uses when already on that page.
        select(item.result.ref);
        void navigate("/topology");
        break;
      case "action":
        item.action.perform();
        break;
      case "cluster-switch":
        switchToCluster(item.clusterId);
        break;
      case "cluster-entity":
        // Switch context to the hit's cluster, then focus the entity within
        // it — the drilled topology page reads both the `?cluster` param and
        // the shared selection.
        select(item.hit.ref);
        switchToCluster(item.hit.clusterId);
        break;
    }
    close();
  }

  function handleInputKeyDown(event: ReactKeyboardEvent<HTMLInputElement>): void {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setHighlighted((h) => (items.length === 0 ? 0 : (h + 1) % items.length));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setHighlighted((h) => (items.length === 0 ? 0 : (h - 1 + items.length) % items.length));
    } else if (event.key === "Enter") {
      event.preventDefault();
      const item = items[highlighted];
      if (item) activate(item);
    }
  }

  function itemLabel(item: PaletteItem): string {
    switch (item.kind) {
      case "entity":
        return item.result.label;
      case "action":
        return item.action.label;
      case "cluster-switch":
        return `Switch to ${item.clusterName}`;
      case "cluster-entity":
        return item.hit.label;
    }
  }

  function itemMeta(item: PaletteItem): string {
    switch (item.kind) {
      case "entity":
        return [item.result.kind, item.result.node].filter(Boolean).join(" · ");
      case "action":
        return item.action.hint ?? "Action";
      case "cluster-switch":
        return "Switch cluster";
      case "cluster-entity":
        return [item.hit.kind, item.hit.node, item.hit.clusterName].filter(Boolean).join(" · ");
    }
  }

  function itemTag(item: PaletteItem): string {
    switch (item.kind) {
      case "entity":
        return "entity";
      case "action":
        return "action";
      case "cluster-switch":
        return "cluster";
      case "cluster-entity":
        return item.hit.clusterName;
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) onOpenChange(true);
        else close();
      }}
    >
      <DialogContent aria-describedby="command-palette-description" className="top-1/4 -translate-y-0">
        <DialogTitle>Command palette</DialogTitle>
        <DialogDescription id="command-palette-description">
          Search entities by name, MAC, IP, VMID, or comment — or run an action.
        </DialogDescription>
        <input
          autoFocus
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
          }}
          onKeyDown={handleInputKeyDown}
          placeholder="Search entities or run a command…"
          aria-label="Command palette input"
          className="mt-3 w-full rounded border border-slate-300 bg-transparent px-2 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700"
        />
        <ul className="mt-3 max-h-80 overflow-y-auto text-sm">
          {isFetching && <li className="px-2 py-1.5 text-slate-600 dark:text-slate-400">Searching…</li>}
          {!isFetching && query.trim() !== "" && items.length === 0 && (
            <li className="px-2 py-1.5 text-slate-600 dark:text-slate-400">No matches.</li>
          )}
          {items.map((item, index) => (
            <li key={itemKey(item)}>
              <button
                type="button"
                onMouseEnter={() => {
                  setHighlighted(index);
                }}
                onClick={() => {
                  activate(item);
                }}
                className={clsx(
                  "flex w-full items-center justify-between gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800",
                  index === highlighted && "bg-slate-100 dark:bg-slate-800",
                  item.kind === "cluster-switch" && "font-medium text-accent-600 dark:text-accent-400",
                )}
              >
                <span className="truncate">
                  <span className="font-medium text-slate-800 dark:text-slate-100">{itemLabel(item)}</span>
                  <span className="ml-2 text-xs text-slate-600 dark:text-slate-400">{itemMeta(item)}</span>
                </span>
                <span className="shrink-0 text-xs uppercase tracking-wide text-slate-600 dark:text-slate-400">{itemTag(item)}</span>
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
