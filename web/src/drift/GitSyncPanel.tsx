// T-3001: `GET /gitsync/status` as a panel — repo, ref, path, last sync, last
// error, and the draft the sync currently has open.
//
// The panel has no controls at all, and that is the contract, not an omission:
// there is deliberately no route that triggers a sync, applies its draft, or
// changes the remote (docs/api.md §"Git spec sync"). The draft it opens is an
// ordinary changeset and goes out through the ordinary review screen, linked
// below.
//
// The reason this panel exists: a `[gitsync]` configuration that is failing —
// a bad ref, an unreachable remote, an auth failure — was previously visible
// only in the daemon's journal. It renders the daemon's own `lastError` text,
// never a generic "sync failed", because the daemon is the only thing that
// knows which of those it was.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { HelpAnchor } from "../help/HelpAnchor";
import type { GitSyncStatus } from "../api/gitsync";
import { instantLabel, type GitSyncState } from "./gitsyncState";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs uppercase tracking-wide text-slate-500 dark:text-slate-400">{label}</dt>
      <dd className="text-sm text-slate-800 dark:text-slate-100">{children}</dd>
    </div>
  );
}

/** Text for a value the daemon omitted. Styled as absent so it can never be
 * mistaken for the value itself. */
function Absent({ children }: { children: ReactNode }) {
  return <span className="italic text-slate-500 dark:text-slate-400">{children}</span>;
}

function StatusDetails({ status }: { status: GitSyncStatus }) {
  const signed = status.requireSignedCommits;
  return (
    <>
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="Repository">
          {status.remote === undefined || status.remote === "" ? (
            <Absent>not reported</Absent>
          ) : (
            <code className="font-mono text-xs break-all">{status.remote}</code>
          )}
        </Field>
        <Field label="Ref">
          {status.ref === undefined || status.ref === "" ? (
            <Absent>not reported</Absent>
          ) : (
            <code className="font-mono text-xs">{status.ref}</code>
          )}
        </Field>
        <Field label="Document path">
          {status.path === undefined || status.path === "" ? (
            <Absent>not reported</Absent>
          ) : (
            <code className="font-mono text-xs break-all">{status.path}</code>
          )}
        </Field>
        <Field label="Last fetched commit">
          {status.lastFetchedSha === undefined || status.lastFetchedSha === "" ? (
            <Absent>nothing fetched yet</Absent>
          ) : (
            <code className="font-mono text-xs">{status.lastFetchedSha.slice(0, 12)}</code>
          )}
        </Field>
        <Field label="Last fetch attempt">{instantLabel(status.lastFetchAt, "never attempted")}</Field>
        <Field label="Last successful sync">{instantLabel(status.lastSuccessAt, "no successful sync yet")}</Field>
        <Field label="Poll interval">
          {status.pollIntervalSeconds === undefined || status.pollIntervalSeconds === 0 ? (
            <Absent>not reported</Absent>
          ) : (
            `${String(status.pollIntervalSeconds)}s`
          )}
        </Field>
        <Field label="Signed commits">
          {signed ? "required — an unverifiable signature refuses the commit" : "not required"}
        </Field>
        <Field label="Last verified signer">
          {!signed ? (
            <Absent>not applicable</Absent>
          ) : status.lastSigner === undefined || status.lastSigner === "" ? (
            <Absent>none recorded</Absent>
          ) : (
            status.lastSigner
          )}
        </Field>
      </dl>

      <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <h3 className="text-sm font-semibold">Plan from the last cycle</h3>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
            {status.planOpCount === 0
              ? "No operations — the document and the cluster agreed."
              : `${String(status.planOpCount)} operation${status.planOpCount === 1 ? "" : "s"} the document would make.`}
          </p>
          {status.plan !== undefined && status.plan.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1">
              {status.plan.map((op) => (
                <li key={op} className="font-mono text-xs text-slate-700 dark:text-slate-200">
                  {op}
                </li>
              ))}
            </ul>
          )}
        </div>
        <div>
          <h3 className="text-sm font-semibold">Not in the document</h3>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
            {status.notInSpec === undefined || status.notInSpec.length === 0
              ? "Nothing — every managed entity on the cluster appears in the document."
              : `${String(status.notInSpec.length)} live entit${status.notInSpec.length === 1 ? "y is" : "ies are"} absent from the document. Reported, never deleted.`}
          </p>
          {status.notInSpec !== undefined && status.notInSpec.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1">
              {status.notInSpec.map((ref) => (
                <li key={ref} className="font-mono text-xs text-slate-700 dark:text-slate-200">
                  {ref}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {status.openChangesetId !== undefined && status.openChangesetId !== "" && (
        <div className="mt-4 rounded-md border border-slate-200 p-3 dark:border-slate-800">
          <h3 className="text-sm font-semibold">The sync has a draft open</h3>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
            {status.openChangesetReason ?? "The document and the cluster disagree."}
          </p>
          <Link
            to={`/changesets/${encodeURIComponent(status.openChangesetId)}/review`}
            className="mt-2 inline-block text-sm font-medium text-accent-700 underline dark:text-accent-300"
          >
            Review the draft
          </Link>
          <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
            It is an ordinary changeset: vnprox staged it and applied nothing. Applying and confirming it are your
            own steps, in the ordinary review screen.
          </p>
        </div>
      )}

      {status.issues !== undefined && status.issues.length > 0 && (
        <ul className="mt-4 flex flex-col gap-2">
          {status.issues.map((issue) => (
            <li
              key={`${issue.check}:${issue.detail}`}
              className="rounded-md border border-amber-300 bg-amber-50 p-2 text-sm dark:border-amber-900 dark:bg-amber-950/40"
            >
              <code className="font-mono text-xs text-slate-600 dark:text-slate-300">{issue.check}</code>{" "}
              <span className="text-xs uppercase tracking-wide text-slate-500 dark:text-slate-400">
                {issue.severity}
              </span>
              <p className="mt-1 text-slate-800 dark:text-slate-100">{issue.detail}</p>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export function GitSyncPanel({ state }: { state: GitSyncState }) {
  return (
    <section aria-labelledby="gitsync-heading">
      <h2 id="gitsync-heading" className="flex items-center gap-2 text-lg font-semibold">
        Spec source — git sync
        <HelpAnchor topic="gitsync-status" />
      </h2>

      {state.kind === "loading" && (
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Reading the git sync status…</p>
      )}

      {state.kind === "unreadable" && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-slate-300 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-900"
        >
          <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">
            Could not read the git sync status
          </p>
          <p className="mt-1 text-sm text-slate-700 dark:text-slate-200">{state.message}</p>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
            This is not the same as the sync being off — vnprox could not ask, so whatever the sync is doing, this
            screen cannot currently say.
          </p>
        </div>
      )}

      {state.kind === "not-configured" && (
        <div className="mt-2 rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-slate-900/60">
          <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">Git sync is not configured</p>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
            There is no <code className="font-mono text-xs">[gitsync]</code> section in this daemon&apos;s
            configuration. Nothing is fetched, no endpoint is contacted, and no draft is opened from a repository.
            The spec position below, if any, comes from the pinned document instead.
          </p>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Turning it on is a change to <code className="font-mono">vnprox.toml</code> on the node — there is
            deliberately no route that configures or triggers a sync from here.
          </p>
        </div>
      )}

      {state.kind === "failing" && (
        <>
          <div
            role="alert"
            className="mt-2 rounded-md border border-red-300 bg-red-50 p-3 dark:border-red-900 dark:bg-red-950/40"
          >
            <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">
              The last sync cycle failed
            </p>
            {/* The daemon's own message, verbatim. A bad ref, an unreachable
             * remote and an auth failure are different problems, and only
             * this string distinguishes them. */}
            <p className="mt-1 text-sm text-slate-800 dark:text-slate-100">{state.message}</p>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              The sync retries on its next tick. A failing cycle stages nothing, so the details below are from the
              last cycle that got that far.
            </p>
          </div>
          <div className="mt-3">
            <StatusDetails status={state.status} />
          </div>
        </>
      )}

      {state.kind === "healthy" && (
        <div className="mt-2">
          <StatusDetails status={state.status} />
        </div>
      )}
    </section>
  );
}
