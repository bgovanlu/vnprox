// T-903: the ⌘K/Ctrl+K command palette. Merges SpotlightSearch's fuzzy
// entity search (GET /inventory/search, docs/features/topology.md §2) with
// every currently-registered palette action (actions.ts) in one dialog —
// "every page action gets a palette verb" per the task card. Mounted once,
// app-wide (AppShell.tsx), like ShortcutHelpDialog and ChangesetDrawer, so
// it's reachable from any page, not just Topology.
import { useEffect, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import clsx from "clsx";
import { useNavigate } from "react-router-dom";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import type { SearchResult } from "../api/types";
import { useSearchQuery } from "../topology/queries";
import { useTopologyStore } from "../topology/store";
import { useAllPaletteActions, type PaletteAction } from "./actions";

export interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type PaletteItem =
  | { readonly kind: "entity"; readonly result: SearchResult }
  | { readonly kind: "action"; readonly action: PaletteAction };

function itemKey(item: PaletteItem): string {
  return item.kind === "entity" ? `entity:${item.result.ref}` : `action:${item.action.id}`;
}

function matchesQuery(action: PaletteAction, query: string): boolean {
  if (!query) return true;
  const needle = query.toLowerCase();
  if (action.label.toLowerCase().includes(needle)) return true;
  if (action.hint?.toLowerCase().includes(needle)) return true;
  return action.keywords?.some((k) => k.toLowerCase().includes(needle)) ?? false;
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

  const items: PaletteItem[] = useMemo(
    () => [
      ...entityResults.map((result): PaletteItem => ({ kind: "entity", result })),
      ...actionResults.map((action): PaletteItem => ({ kind: "action", action })),
    ],
    [entityResults, actionResults],
  );

  // Keep the highlighted row in range as the merged list changes size
  // (typing narrows/widens both the entity and action halves at once).
  useEffect(() => {
    setHighlighted(0);
  }, [query, items.length]);

  function close(): void {
    onOpenChange(false);
    setQuery("");
    setHighlighted(0);
  }

  function activate(item: PaletteItem): void {
    if (item.kind === "entity") {
      // Mirrors TopBar.tsx's global search box: the dialog is mounted
      // app-wide, not scoped to the Topology page, so selecting an entity
      // sets the shared selection before navigating there — the same
      // mechanism SpotlightSearch's onSelect uses when already on that page.
      select(item.result.ref);
      void navigate("/topology");
    } else {
      item.action.perform();
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
          {isFetching && <li className="px-2 py-1.5 text-slate-400">Searching…</li>}
          {!isFetching && query.trim() !== "" && items.length === 0 && (
            <li className="px-2 py-1.5 text-slate-400">No matches.</li>
          )}
          {items.map((item, index) => {
            const label = item.kind === "entity" ? item.result.label : item.action.label;
            const meta =
              item.kind === "entity"
                ? [item.result.kind, item.result.node].filter(Boolean).join(" · ")
                : (item.action.hint ?? "Action");
            return (
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
                  )}
                >
                  <span className="truncate">
                    <span className="font-medium text-slate-800 dark:text-slate-100">{label}</span>
                    <span className="ml-2 text-xs text-slate-400">{meta}</span>
                  </span>
                  <span className="shrink-0 text-xs uppercase tracking-wide text-slate-400">{item.kind}</span>
                </button>
              </li>
            );
          })}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
