import { useState } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { HelpAnchor } from "../help/HelpAnchor";
import { useSearchQuery } from "./queries";

export interface SpotlightSearchProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (ref: string) => void;
}

/** The `/` spotlight search (docs/features/topology.md §2: "fuzzy across
 * names, MACs, IPs, VMIDs, comments; selecting focuses + highlights the
 * entity"). Calls GET /inventory/search live as the user types. */
export function SpotlightSearch({ open, onOpenChange, onSelect }: SpotlightSearchProps) {
  const [query, setQuery] = useState("");
  const { data, isFetching } = useSearchQuery(query);
  const results = data?.results ?? [];

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) setQuery("");
      }}
    >
      <DialogContent aria-describedby="spotlight-search-description" className="top-1/4 -translate-y-0">
        <div className="flex items-center gap-1.5">
          <DialogTitle>Search</DialogTitle>
          <HelpAnchor topic="spotlight-search" />
        </div>
        <DialogDescription id="spotlight-search-description">
          Search by name, MAC, IP, VMID, or comment.
        </DialogDescription>
        <input
          autoFocus
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
          }}
          placeholder="web01, 192.168.1.10, aa:bb:cc:..."
          className="mt-3 w-full rounded border border-slate-300 bg-transparent px-2 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700"
        />
        <ul className="mt-3 max-h-72 overflow-y-auto text-sm">
          {isFetching && <li className="px-2 py-1.5 text-slate-400">Searching…</li>}
          {!isFetching && query.trim() !== "" && results.length === 0 && (
            <li className="px-2 py-1.5 text-slate-400">No matches.</li>
          )}
          {results.map((r) => (
            <li key={r.ref}>
              <button
                type="button"
                onClick={() => {
                  onSelect(r.ref);
                  onOpenChange(false);
                  setQuery("");
                }}
                className="flex w-full items-center justify-between gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                {/* The separating spaces are text nodes, not just `ml-*`
                    margins. Margins position the badges visually but put no
                    whitespace in the accessible name, so this button used to
                    announce as "app01guest· pve1 name" — a screen reader reads
                    "app01guest" as one word, and the entity name and its kind
                    are run together. Found by T-2108: diagnose.spec.ts and
                    guest-interior.spec.ts both look for the button by its
                    accessible name "app01 guest", which is what it should have
                    been announcing all along. */}
                <span className="truncate">
                  <span className="font-medium text-slate-800 dark:text-slate-100">{r.label}</span>{" "}
                  <span className="ml-2 text-xs text-slate-400">{r.kind}</span>{" "}
                  {r.node && <span className="ml-1 text-xs text-slate-400">· {r.node}</span>}
                </span>
                <span className="shrink-0 text-xs text-slate-400">{r.matchedField}</span>
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
