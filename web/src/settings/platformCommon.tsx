// SPDX-License-Identifier: Apache-2.0

// Shared chrome for the Platform panel's four sections (T-3003).
//
// The one non-obvious piece here is `RefusalNotice`. Three of these four
// route families refuse a caller for *structural* reasons that are not
// failures of the thing being looked at:
//
//   - 403 — the session lacks the capability the route is gated on
//     (`automation` for webhooks, `audit` for `/doctor/live`).
//   - 404 — the route is not mounted at all, because its service is not
//     wired on this daemon (`mountPluginRoutes`/`mountDoctorRoutes` return
//     early for a nil service, and the router's own NotFound answers).
//
// Rendering either as "nothing here" or as "it failed" is the defect family
// planning/tasks/phase-29.md's wave-4 record generalises: an absent or
// unknown state shown as a definite one. So every one of them renders the
// daemon's own message verbatim, alongside the status and error code, and
// says which of the two situations it is.
import type { ReactNode } from "react";
import clsx from "clsx";
import { ApiError } from "../api/client";
import { HelpAnchor } from "../help/HelpAnchor";

export function PlatformSection({
  title,
  description,
  helpTopic,
  actions,
  children,
}: {
  title: string;
  description?: ReactNode;
  helpTopic: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="rounded-lg border border-slate-200 p-4 dark:border-slate-700">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-slate-800 dark:text-slate-100">
            {title}
            <HelpAnchor topic={helpTopic} />
          </h2>
          {description !== undefined && (
            <p className="mt-0.5 text-xs text-slate-600 dark:text-slate-400">{description}</p>
          )}
        </div>
        {actions}
      </div>
      <div className="mt-3">{children}</div>
    </section>
  );
}

/** Why an operator cannot see or do something here, in the daemon's own
 * words. `unavailableHint`/`forbiddenHint` let each section explain what its
 * particular 404/403 means without this component guessing. */
export function RefusalNotice({
  error,
  forbiddenHint,
  unavailableHint,
  testId,
}: {
  error: unknown;
  forbiddenHint?: ReactNode;
  unavailableHint?: ReactNode;
  testId: string;
}) {
  const api = error instanceof ApiError ? error : undefined;
  const kind = api === undefined ? "unknown" : api.status === 403 ? "forbidden" : api.status === 404 ? "unavailable" : "error";

  const heading =
    kind === "forbidden"
      ? "This session may not read this"
      : kind === "unavailable"
        ? "Not available on this daemon"
        : "The request did not succeed";

  const hint = kind === "forbidden" ? forbiddenHint : kind === "unavailable" ? unavailableHint : undefined;

  return (
    <div
      role="status"
      data-testid={testId}
      data-refusal-kind={kind}
      className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-500/40 dark:bg-amber-500/10"
    >
      <p className="font-medium text-amber-900 dark:text-amber-200">{heading}</p>
      <p className="mt-1 text-amber-900/90 dark:text-amber-100/90">
        {/* The daemon's own message, unedited. A generic "something went
         * wrong" here would erase exactly the detail the operator came for
         * — T-2905's refusals name the policy AND the config knob. */}
        <span data-testid={`${testId}-message`}>{api?.message ?? (error instanceof Error ? error.message : String(error))}</span>
      </p>
      {/* T-3406-followup-01: the /80-opacity wash below measured 4.46:1
       * against amber-50 in light mode (axe: 4.48, under the 4.5:1 floor) —
       * computed via the same OKLCH-alpha-blend method as index.css's
       * demo-amber wash comment: a partial-opacity text color barely
       * darkens regardless of which step it is mixed from, the T-3406
       * lesson this follow-up's own card names. Solid amber-800 clears it at
       * 6.84:1; dark mode's amber-200/70 already passes at 6.77:1 so is left
       * as-is. */}
      {api !== undefined && (
        <p className="mt-1 font-mono text-xs text-amber-800 dark:text-amber-200/70">
          HTTP {api.status} · {api.code}
        </p>
      )}
      {/* Same wash, same fix as the HTTP-status line above — amber-900/80
       * on amber-50 measures 3.43:1 at this 12px size, well under the
       * 4.5:1 floor, while solid amber-900 clears it at 8.77:1. Worth
       * naming why it survived the pass that fixed its sibling four lines
       * up: the axe sweep only ever rendered this component without a
       * `hint`, so the node carrying the defect was never in the DOM being
       * scanned. A sweep proves what it renders, not what a component can
       * render — the same blind spot that hid the disabled/enabled Delete
       * button asymmetry from the original T-3406 sweep. Dark mode's
       * amber-100/80 is on a dark surface and already passes. */}
      {hint !== undefined && <div className="mt-2 text-xs text-amber-900 dark:text-amber-100/80">{hint}</div>}
    </div>
  );
}

/** A `<time>` rendering of a unix-seconds instant. `undefined` renders the
 * caller's word for absence, never an empty cell — a blank is indistinguishable
 * from a value that failed to load. */
export function UnixTime({ at, absent = "—" }: { at: number | undefined; absent?: string }) {
  if (at === undefined) {
    return <span className="text-slate-600 dark:text-slate-400">{absent}</span>;
  }
  const d = new Date(at * 1000);
  return <time dateTime={d.toISOString()}>{d.toLocaleString()}</time>;
}

/** A neutral pill for a scope/capability/extension-point name. */
export function ScopeChip({ name, tone = "neutral" }: { name: string; tone?: "neutral" | "removed" }) {
  return (
    <span
      className={clsx(
        "rounded px-1.5 py-0.5 font-mono text-[11px]",
        tone === "removed"
          ? "bg-slate-100 text-slate-600 line-through dark:bg-slate-800 dark:text-slate-400"
          : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300",
      )}
    >
      {name}
    </span>
  );
}

/** Renders a list of names as chips, or an explicit "none" — an empty list is
 * a fact and should read as one. */
export function ScopeChips({ names, empty = "none" }: { names: readonly string[]; empty?: string }) {
  if (names.length === 0) {
    return <span className="text-xs italic text-slate-600 dark:text-slate-400">{empty}</span>;
  }
  return (
    <span className="inline-flex flex-wrap gap-1">
      {names.map((n) => (
        <ScopeChip key={n} name={n} />
      ))}
    </span>
  );
}
