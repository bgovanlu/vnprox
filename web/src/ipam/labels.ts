// SPDX-License-Identifier: Apache-2.0

// Human-readable labels + Tailwind color classes for IPAM cell states and
// confidence labels (docs/features/ipam.md §2's grid legend: "free /
// allocated / observed-unallocated / reserved / gateway / conflict").
// Framework-free so it's directly Vitest-able.
import type { IpamCellState, IpamConfidence } from "../api/types";

export function stateLabel(state: IpamCellState): string {
  switch (state) {
    case "free":
      return "Free";
    case "allocated":
      return "Allocated";
    case "reserved":
      return "Reserved";
    case "observed":
      return "Observed (unallocated)";
    case "gateway":
      return "Gateway";
    case "conflict":
      return "Conflict";
    default:
      return state;
  }
}

export function confidenceLabel(confidence: IpamConfidence): string {
  switch (confidence) {
    case "allocated":
      return "allocated";
    case "observed":
      return "observed";
    case "both":
      return "allocated + observed";
    case "conflict":
      return "conflict";
    default:
      return "";
  }
}

/** Per-state color coding for the address list (docs/features/ipam.md §2),
 * light + dark mode both handled per docs/development.md's theme-aware
 * convention. The single source of truth shared by the row swatch, the state
 * pill, and the summary strip's segments.
 *
 * `chip` styles the pill next to an address; `swatch` is the solid rail color
 * on the left of each row and the segment/legend color in the summary strip.
 */
// T-4204: `conflict` is the one cell state that is genuinely a health
// problem (two records claiming the same address) rather than a category
// of address, so it alone moves onto the semantic status scale
// (`status-critical`); free/allocated/reserved/observed/gateway stay their
// own taxonomy colours — which KIND of cell this is, not how healthy it
// is — same reasoning as the topology map's per-kind accents.
export const stateChipClasses: Record<IpamCellState, string> = {
  free: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300",
  allocated: "bg-accent-soft text-accent-fg",
  reserved: "bg-sky-100 text-sky-800 dark:bg-sky-950/60 dark:text-sky-200",
  observed: "bg-amber-100 text-amber-800 dark:bg-amber-950/60 dark:text-amber-200",
  gateway: "bg-violet-100 text-violet-800 dark:bg-violet-950/60 dark:text-violet-200",
  conflict: "bg-status-critical-soft text-status-critical",
};

/** Solid fill classes for the row rail swatch and the summary-strip segments,
 * keyed by state. */
export const stateSwatchClasses: Record<IpamCellState, string> = {
  free: "bg-slate-300 dark:bg-slate-600",
  allocated: "bg-accent-500",
  reserved: "bg-sky-500",
  observed: "bg-amber-500",
  gateway: "bg-violet-500",
  conflict: "bg-status-critical",
};
