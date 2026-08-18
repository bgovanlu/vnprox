import type { ReactNode } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
  /** T-905: compact/comfortable spacing (density.ts) — "comfortable" is
   * this component's original `min-h-[16rem] p-10` scale, so the prop is
   * additive. "compact" is for a small inline empty state (e.g. a tile in
   * a dense dashboard grid) rather than a full-page placeholder. Defaults
   * to the ambient `<DensityProvider>` in scope. */
  density?: Density;
}

const DENSITY_CLASSES: Record<Density, string> = {
  comfortable: "min-h-[16rem] gap-2 p-10",
  compact: "min-h-[8rem] gap-1 p-4",
};

/** Generic "nothing here (yet)" panel — used by every placeholder page in
 * this task, and by the keyboard framework's "not yet implemented"
 * affordances elsewhere in the app. */
export function EmptyState({ title, description, action, className, density }: EmptyStateProps) {
  const resolvedDensity = useDensity(density);
  return (
    <div
      data-density={resolvedDensity}
      className={clsx(
        // T-3405: larger radius to match Dialog/Drawer/Toast's softer look.
        "flex h-full flex-col items-center justify-center rounded-xl border border-dashed text-center",
        DENSITY_CLASSES[resolvedDensity],
        "border-slate-300 dark:border-slate-700",
        className,
      )}
    >
      <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{title}</h2>
      {description ? (
        <p className="max-w-md text-sm text-slate-500 dark:text-slate-400">{description}</p>
      ) : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
