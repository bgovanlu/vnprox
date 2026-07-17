import clsx from "clsx";
import type { TopologyViewMode } from "./store";

const MODES: { mode: TopologyViewMode; label: string }[] = [
  { mode: "switch", label: "Switch" },
  { mode: "graph", label: "Graph" },
];

export interface ViewModeToggleProps {
  value: TopologyViewMode;
  onChange: (mode: TopologyViewMode) => void;
}

/** Segmented Switch/Graph control for the Topology page header — picks which
 * rendering of the same GET /topology data is shown (docs/features/
 * topology.md §2). */
export function ViewModeToggle({ value, onChange }: ViewModeToggleProps) {
  return (
    <div
      role="radiogroup"
      aria-label="Topology view mode"
      className="flex gap-0.5 rounded-md border border-slate-200 bg-white/90 p-0.5 shadow-sm dark:border-slate-700 dark:bg-slate-900/90"
    >
      {MODES.map(({ mode, label }) => {
        const active = value === mode;
        return (
          <button
            key={mode}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => {
              onChange(mode);
            }}
            className={clsx(
              "rounded px-3 py-1 text-xs font-medium transition-colors",
              active
                ? // T-905 (axe): accent-700 not -600 — white-on-accent-600 is 3.76:1, below AA.
                  "bg-accent-700 text-white"
                : "text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800",
            )}
          >
            {label}
          </button>
        );
      })}
    </div>
  );
}
