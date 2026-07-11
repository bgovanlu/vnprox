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

/** Grid cell color classes, keyed by state — the single source of truth
 * for T-405 acceptance criterion 1's per-cell color coding (light + dark
 * mode both handled, per docs/development.md's theme-aware convention). */
export const cellStateClasses: Record<IpamCellState, string> = {
  free: "bg-slate-100 dark:bg-slate-800 border-slate-200 dark:border-slate-700",
  allocated: "bg-accent-100 dark:bg-accent-900/50 border-accent-300 dark:border-accent-700",
  reserved: "bg-sky-100 dark:bg-sky-900/50 border-sky-300 dark:border-sky-700",
  observed: "bg-amber-100 dark:bg-amber-900/50 border-amber-300 dark:border-amber-700",
  gateway: "bg-violet-100 dark:bg-violet-900/50 border-violet-300 dark:border-violet-700",
  conflict: "bg-red-200 dark:bg-red-900/60 border-red-400 dark:border-red-600",
};
