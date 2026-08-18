import type { HTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from "react";
import clsx from "clsx";
import { DensityProvider, useDensity, type Density } from "./density";

export interface TableProps extends HTMLAttributes<HTMLTableElement> {
  /** T-905: compact/comfortable spacing for this table and every
   * `TableHead`/`TableCell` nested inside it (density.ts's ambient
   * `DensityProvider` — no prop drilling needed on the individual cells).
   * T-3405 (docs/development.md "Visual language" — "generous row height at
   * comfortable density") widened comfortable's row padding to `px-4 py-3`;
   * "compact" keeps the pre-T-3405 `px-2 py-1 text-xs` scale so the two
   * densities stay visibly distinct and compact keeps tightening relative
   * to comfortable, not just holding still. */
  density?: Density;
}

export function Table({ className, density, children, ...props }: TableProps) {
  const resolved = useDensity(density);
  return (
    <DensityProvider density={resolved}>
      <div className="w-full overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800">
        <table
          data-density={resolved}
          className={clsx("w-full border-collapse text-left", resolved === "compact" ? "text-xs" : "text-sm", className)}
          {...props}
        >
          {children}
        </table>
      </div>
    </DensityProvider>
  );
}

// T-3405: quieter header — no uppercase/letter-spacing shouting, muted text
// over a near-transparent tint rather than a solid filled band; the hairline
// separation from the body comes from `TableHead`'s `border-b` (cell-level,
// so it survives `border-collapse`) rather than a border on `<thead>` itself.
export function TableHeader({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    // T-3406: text-slate-500 measures ~4.3:1 wherever this table has no
    // white card of its own behind it — `Table`'s own wrapper `<div>`
    // above carries only a border, no background, so any page that drops
    // a `<Table>` straight onto AppShell's `bg-slate-100` canvas (most of
    // the ~26 call sites: AuditPage, ConntrackExplorer, GuestsPage,
    // FlowExplorer, EdgeCockpit, FwLogViewer, the SDN views, ...) composites
    // this header's own near-transparent `bg-slate-50/60` tint down to
    // essentially the same background PageHeader's description line failed
    // against, for the same reason (found by the same T-3406 axe sweep).
    // slate-600 clears AA; dark mode (slate-400 on the slate-900/40 tint)
    // is unaffected.
    <thead
      className={clsx("bg-slate-50/60 text-slate-600 dark:bg-slate-900/40 dark:text-slate-400", className)}
      {...props}
    />
  );
}

// T-3405: hairline row borders, no zebra striping (no odd/even background).
export function TableBody({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={clsx("divide-y divide-slate-200 dark:divide-slate-800", className)} {...props} />;
}

export function TableRow({ className, ...props }: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={clsx("hover:bg-slate-50 dark:hover:bg-slate-800/40", className)}
      {...props}
    />
  );
}

export interface TableCellDensityProps {
  /** Per-cell override; normally left unset so it inherits the enclosing
   * `<Table density=…>`'s ambient value (density.ts). */
  density?: Density;
}

export function TableHead({
  className,
  density,
  ...props
}: ThHTMLAttributes<HTMLTableCellElement> & TableCellDensityProps) {
  const resolved = useDensity(density);
  return (
    <th
      className={clsx(
        resolved === "compact" ? "px-2 py-1" : "px-4 py-3",
        "border-b border-slate-200 font-medium dark:border-slate-800",
        className,
      )}
      {...props}
    />
  );
}

export function TableCell({
  className,
  density,
  ...props
}: TdHTMLAttributes<HTMLTableCellElement> & TableCellDensityProps) {
  const resolved = useDensity(density);
  return (
    <td
      className={clsx(
        resolved === "compact" ? "px-2 py-1" : "px-4 py-3",
        "text-slate-700 dark:text-slate-200",
        className,
      )}
      {...props}
    />
  );
}
