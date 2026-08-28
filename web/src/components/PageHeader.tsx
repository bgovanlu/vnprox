// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from "react";
import clsx from "clsx";

export interface PageHeaderProps {
  /** The page's single `<h1>` content (T-3404: "every page keeps exactly
   * one <h1>" — several e2e specs assert on headings, docs/development.md's
   * TypeScript-standards section explains why). Accepts a `ReactNode` (not
   * just a string) so a title can carry an inline `<HelpAnchor>` the way
   * several pages already did before this component existed — the `<h1>`
   * itself always wraps its children in a `flex items-center gap-2` row so
   * that pattern lines up without every call site repeating the wrapper. */
  title: ReactNode;
  /** The description line directly under the title, e.g. "Network at a
   * glance…". Rendered as a single `<p>` — pass plain text or inline
   * markup, not a block-level layout. */
  description?: ReactNode;
  /** Right-aligned page-level controls: pill-shaped `Button`s
   * (`shape="pill"`, docs/development.md "Visual language") for whole-page
   * actions, or plain form controls (a node/scope `<select>`) that don't
   * need the pill treatment. Wrapped in a `flex flex-wrap items-center
   * gap-2` row. */
  actions?: ReactNode;
  /** The underlined tab row (T-3404's shared `Tabs` wrapper's `<TabsList>`)
   * for pages with tab-like sub-navigation. Rendered on its own row below
   * the title/actions row, matching the reference dashboard's "Balances"
   * screen (docs/development.md "Big page title + pill buttons" /
   * "Underlined tabs"). Omit for pages with no sub-navigation. */
  tabs?: ReactNode;
  className?: string;
}

/** The shared page-header pattern every routed page (except Login and the
 * full-bleed Topology map — T-3404's explicit exemptions) renders through:
 * a large title, an optional description line, right-aligned page actions,
 * and an optional underlined tab row. Renders the page's *only* `<h1>` —
 * callers must not render a second one. */
export function PageHeader({ title, description, actions, tabs, className }: PageHeaderProps) {
  return (
    <div className={clsx("flex flex-col gap-3", className)}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="flex items-center gap-2 text-xl font-semibold text-slate-900 dark:text-slate-100">
            {title}
          </h1>
          {description !== undefined && (
            // T-3406: text-slate-500 measures 4.34:1 against this row's
            // ambient background — AppShell's `<main>` is `bg-slate-100`
            // in light mode, and PageHeader (unlike most other
            // text-slate-500-on-light-background call sites in this
            // codebase, which sit inside a `bg-white` dialog/drawer/card)
            // renders directly on that canvas with no wrapper of its own —
            // below the 4.5:1 AA floor, found by this phase's full-sweep
            // axe run (T-3406) landing on every one of the ~24 pages this
            // component now serves, none of which had ever been axe-scanned
            // in light mode before (the app defaults to dark — see
            // forceLightTheme's comment elsewhere in this repo). slate-600
            // clears AA against slate-100 with margin; dark mode is
            // unaffected (dark:bg-slate-900 already gives slate-400 plenty
            // of contrast).
            <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">{description}</p>
          )}
        </div>
        {actions !== undefined && (
          <div className="flex flex-wrap items-center gap-2">{actions}</div>
        )}
      </div>
      {tabs !== undefined && <div>{tabs}</div>}
    </div>
  );
}
