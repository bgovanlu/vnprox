// SPDX-License-Identifier: Apache-2.0

// T-4208, promoted from eight independent hand-rolled `<dl>`s that had all
// converged on the same CSS-grid trick without sharing it:
// topology/InspectorPanel.tsx, topology/InspectorCompareView.tsx,
// topology/FlowPairPanel.tsx, topology/layers/PodDrilldown.tsx (twice),
// topology/federation/ClusterCapsule.tsx, topology/BondLacpSection.tsx,
// drift/GitSyncPanel.tsx, and onboarding/OnboardingWalkthrough.tsx.
//
// Two shapes recur, and this component is one implementation of both,
// picked by `columns`:
//
//   - `columns={1}` (the common case — InspectorPanel, FlowPairPanel,
//     PodDrilldown): a narrow `grid-cols-[auto_1fr]` list, one row per
//     item, label left / value right.
//   - `columns={2|3}` (ClusterCapsule, OnboardingWalkthrough): several
//     label/value pairs side by side per row.
//
// The `<div className="contents">` wrapper per item is
// OnboardingWalkthrough.tsx:110's own technique, generalized: it lets each
// item's `dt`/`dd` participate directly in the OUTER grid's columns/rows
// (so columns actually align down the list) while still keeping one
// semantic dt/dd pair per list entry, rather than hand-computing which
// grid cell each label and value belongs in.
import type { ReactNode } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";

export interface KeyValueItem {
  key: string;
  label: ReactNode;
  /** `font-mono tabular-nums` — for addresses, MACs, counters (T-4202: "an
   * IP address" is exactly what tabular-nums exists for). */
  mono?: boolean;
  value: ReactNode;
}

export interface KeyValueProps {
  items: readonly KeyValueItem[];
  /** How many label/value pairs sit side by side per row. 1 (default) is
   * the narrow inspector-panel shape; 2/3 is the side-by-side summary
   * shape (ClusterCapsule, OnboardingWalkthrough). */
  columns?: 1 | 2 | 3;
  size?: "sm" | "md";
  density?: Density;
  className?: string;
}

const GRID_COLS: Record<1 | 2 | 3, string> = {
  1: "grid-cols-[auto_1fr]",
  2: "grid-cols-[auto_1fr_auto_1fr]",
  3: "grid-cols-[auto_1fr_auto_1fr_auto_1fr]",
};

const GAP_Y: Record<Density, string> = { comfortable: "gap-y-1.5", compact: "gap-y-0.5" };
const TEXT_SIZE: Record<"sm" | "md", string> = { sm: "text-xs", md: "text-sm" };

/** A label/value list — an inspector panel's field grid, a summary strip's
 * counts, a diagnostic's key facts. Not a Table: no header row, no
 * sort/filter, meant for a handful of fields rather than a data set. */
export function KeyValue({ items, columns = 1, size = "sm", density, className }: KeyValueProps) {
  const resolvedDensity = useDensity(density);
  return (
    <dl
      data-density={resolvedDensity}
      className={clsx("grid gap-x-3", GRID_COLS[columns], GAP_Y[resolvedDensity], TEXT_SIZE[size], className)}
    >
      {items.map((item) => (
        <div className="contents" key={item.key}>
          <dt className="text-fg-muted">{item.label}</dt>
          <dd className={clsx("min-w-0 text-slate-800 dark:text-slate-100", item.mono && "font-mono tabular-nums")}>
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}
