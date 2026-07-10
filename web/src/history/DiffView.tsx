// Unified-diff rendering for the History page (and reusable by any other
// feature that has a docs/api.md `unified` diff string to show). The line
// classification lives in parseDiff.ts so it's unit-testable without
// rendering and this file only exports components.
import clsx from "clsx";
import { parseUnifiedDiff, type DiffLineKind } from "./parseDiff";

const lineClasses: Record<DiffLineKind, string> = {
  header: "text-slate-500 dark:text-slate-400 font-semibold",
  hunk: "bg-sky-50 text-sky-700 dark:bg-sky-950/60 dark:text-sky-300",
  add: "bg-emerald-50 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300",
  remove: "bg-red-50 text-red-800 dark:bg-red-950/60 dark:text-red-300",
  context: "text-slate-600 dark:text-slate-300",
};

export interface DiffViewProps {
  unified: string;
  className?: string;
}

/** Colored, monospace unified-diff block. */
export function DiffView({ unified, className }: DiffViewProps) {
  const lines = parseUnifiedDiff(unified);
  if (lines.length === 0) {
    return (
      <p className={clsx("text-sm text-slate-500 dark:text-slate-400", className)}>
        No differences.
      </p>
    );
  }
  return (
    <pre
      className={clsx(
        "overflow-x-auto rounded-md border border-slate-200 bg-white p-3 text-xs leading-5",
        "dark:border-slate-800 dark:bg-slate-950",
        className,
      )}
    >
      {lines.map((line, i) => (
        <div key={i} className={clsx("whitespace-pre px-1", lineClasses[line.kind])}>
          {line.text || " "}
        </div>
      ))}
    </pre>
  );
}
