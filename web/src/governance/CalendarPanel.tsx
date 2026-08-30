// SPDX-License-Identifier: Apache-2.0

// T-4006: the change-calendar view — every declared freeze window alongside
// every pending scheduled changeset, on one screen, so an operator can see
// WHY a staged apply will be refused (or a schedule they already set is
// about to be) before either happens. Read-only: nothing here enforces
// anything, exactly like PoliciesPanel/CompliancePanel beside it — the
// change engine's own validate stage remains the sole authority.
import { HelpAnchor } from "../help/HelpAnchor";
import { useCalendarQuery } from "./queries";
import { freezeWindowSummary, scheduleInOneOffFreeze, sortSchedulesByWindowStart } from "./calendar";
import { ApiError } from "../api/client";
import type { FreezeWindowView } from "../api/policies";

function SeverityBadge({ severity }: { severity: string }) {
  const isDeny = severity === "deny";
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-xs font-medium ${
        isDeny
          ? "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200"
          : "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-200"
      }`}
    >
      {isDeny ? "deny — blocks" : severity === "warn" ? "warn — annotates only" : severity}
    </span>
  );
}

function FreezeWindowRow({ w }: { w: FreezeWindowView }) {
  const summary = freezeWindowSummary(w);
  return (
    <li className="rounded border border-border p-2" data-testid="freeze-window-row">
      <div className="flex items-center gap-2">
        <span className="font-medium">{w.ruleId}</span>
        <SeverityBadge severity={w.severity} />
      </div>
      <p className="text-fg-muted">{w.description}</p>
      {summary !== undefined ? (
        <p className="mt-1 font-mono text-xs text-fg-body">{summary}</p>
      ) : (
        <p className="mt-1 text-xs italic text-fg-subtle">
          This window's time conditions are too irregular for the calendar to summarize — see the rule's own match
          conditions on the Policies tab.
        </p>
      )}
    </li>
  );
}

export function CalendarPanel() {
  const query = useCalendarQuery();
  const view = query.data;
  const notConfigured = query.error instanceof ApiError && query.error.code === "policy_unavailable";
  const unreadable = query.error !== null && !notConfigured;

  const schedules = sortSchedulesByWindowStart(view?.schedules ?? []);
  const windows = view?.freezeWindows ?? [];

  return (
    <section aria-label="Change calendar" data-testid="calendar-panel" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold">Change calendar</h2>
        <HelpAnchor topic="freeze-windows" />
      </div>
      <p className="text-sm text-fg-muted">
        Every declared freeze window in the installed policy set, alongside every changeset currently scheduled to
        apply in a future maintenance window. Nothing on this page is the enforcement — a freeze blocks (or a
        schedule fires) inside the change engine, at validate/fire time, exactly as it would if this page did not
        exist.
      </p>

      {query.isLoading && <p className="text-sm text-fg-muted">Reading the calendar…</p>}

      {notConfigured && (
        <p className="text-sm text-fg-muted">
          This daemon has no policy store wired, so there are no declared freeze windows to show. Pending schedules,
          if any, still render below.
        </p>
      )}
      {unreadable && (
        <p className="text-sm text-fg-body" role="status">
          The calendar could not be read. The daemon said:{" "}
          {query.error instanceof Error ? query.error.message : "the read failed"}
        </p>
      )}

      {view !== undefined && (
        <>
          <div>
            <h3 className="text-sm font-semibold">Declared freeze windows</h3>
            {windows.length === 0 ? (
              <p className="text-sm text-fg-muted">
                None declared. A freeze window is an ordinary policy rule tagged <code>freeze</code> — see the
                Policies tab.
              </p>
            ) : (
              <ul className="mt-1 flex flex-col gap-1.5 text-sm">
                {windows.map((w) => (
                  <FreezeWindowRow key={w.ruleId} w={w} />
                ))}
              </ul>
            )}
          </div>

          <div>
            <h3 className="text-sm font-semibold">Pending scheduled changesets</h3>
            {schedules.length === 0 ? (
              <p className="text-sm text-fg-muted">Nothing is currently scheduled.</p>
            ) : (
              <ul className="mt-1 flex flex-col gap-1.5 text-sm">
                {schedules.map((s) => {
                  const inFreeze = scheduleInOneOffFreeze(s, windows);
                  return (
                    <li
                      key={s.changesetId}
                      className="rounded border border-border p-2"
                      data-testid="pending-schedule-row"
                    >
                      <p>
                        Changeset <span className="font-mono text-xs">{s.changesetId}</span> fires at{" "}
                        {new Date(s.windowStart * 1000).toLocaleString()}
                      </p>
                      {inFreeze && (
                        <p className="mt-1 text-xs font-medium text-red-700 dark:text-red-300" role="alert">
                          This fire instant falls inside a declared freeze window above — the scheduler will refuse it
                          at fire time unless the freeze is lifted or overridden first.
                        </p>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </>
      )}
    </section>
  );
}
