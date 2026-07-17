import type { HTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from "react";
import clsx from "clsx";
import { DensityProvider, useDensity, type Density } from "./density";

export interface TableProps extends HTMLAttributes<HTMLTableElement> {
  /** T-905: compact/comfortable spacing for this table and every
   * `TableHead`/`TableCell` nested inside it (density.ts's ambient
   * `DensityProvider` — no prop drilling needed on the individual cells).
   * "comfortable" (the table's original `px-3 py-2 text-sm` scale) is the
   * default, so this is purely additive — no existing call site's rendered
   * output changes. "compact" tightens to `px-2 py-1 text-xs` for
   * dense data views (e.g. an audit log or a large firewall rule list). */
  density?: Density;
}

export function Table({ className, density, children, ...props }: TableProps) {
  const resolved = useDensity(density);
  return (
    <DensityProvider density={resolved}>
      <div className="w-full overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
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

export function TableHeader({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead
      className={clsx("bg-slate-100/80 text-slate-600 dark:bg-slate-900/60 dark:text-slate-300", className)}
      {...props}
    />
  );
}

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
      className={clsx(resolved === "compact" ? "px-2 py-1" : "px-3 py-2", "font-medium", className)}
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
        resolved === "compact" ? "px-2 py-1" : "px-3 py-2",
        "text-slate-700 dark:text-slate-200",
        className,
      )}
      {...props}
    />
  );
}
