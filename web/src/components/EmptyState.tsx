import type { ReactNode } from "react";
import clsx from "clsx";

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

/** Generic "nothing here (yet)" panel — used by every placeholder page in
 * this task, and by the keyboard framework's "not yet implemented"
 * affordances elsewhere in the app. */
export function EmptyState({ title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={clsx(
        "flex h-full min-h-[16rem] flex-col items-center justify-center gap-2 rounded-lg border border-dashed",
        "border-slate-300 p-10 text-center dark:border-slate-700",
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
