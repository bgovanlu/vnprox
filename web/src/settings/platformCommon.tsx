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
            <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">{description}</p>
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
      {api !== undefined && (
        <p className="mt-1 font-mono text-xs text-amber-800/80 dark:text-amber-200/70">
          HTTP {api.status} · {api.code}
        </p>
      )}
      {hint !== undefined && <div className="mt-2 text-xs text-amber-900/80 dark:text-amber-100/80">{hint}</div>}
    </div>
  );
}

/** A `<time>` rendering of a unix-seconds instant. `undefined` renders the
 * caller's word for absence, never an empty cell — a blank is indistinguishable
 * from a value that failed to load. */
export function UnixTime({ at, absent = "—" }: { at: number | undefined; absent?: string }) {
  if (at === undefined) {
    return <span className="text-slate-400 dark:text-slate-500">{absent}</span>;
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
          ? "bg-slate-100 text-slate-400 line-through dark:bg-slate-800 dark:text-slate-500"
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
    return <span className="text-xs italic text-slate-400 dark:text-slate-500">{empty}</span>;
  }
  return (
    <span className="inline-flex flex-wrap gap-1">
      {names.map((n) => (
        <ScopeChip key={n} name={n} />
      ))}
    </span>
  );
}
