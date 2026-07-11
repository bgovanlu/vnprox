// T-208's raw editor chrome: node picker context lives in the caller
// (ToolsPage); this component owns one node's load/edit/lint/save/conflict
// flow. Monaco itself is loaded lazily (React.lazy) so opening this panel
// is the only thing that pulls the editor chunk in (AC4).
import { Suspense, lazy } from "react";
import { useSession } from "../../api/useSession";
import { Button } from "../../components/Button";
import { Tooltip } from "../../components/Tooltip";
import { capsForNode } from "../capabilities";
import { useRawEditor } from "./useRawEditor";

const MonacoRawEditor = lazy(() => import("./MonacoRawEditor"));

export interface RawEditorPanelProps {
  node: string;
}

export function RawEditorPanel({ node }: RawEditorPanelProps) {
  const { data: session } = useSession();
  const [state, actions] = useRawEditor(node);
  const canWrite = capsForNode(session, node).netWrite;
  const disabledReason = canWrite
    ? undefined
    : "You do not have network-write access on this node";

  const errorCount = state.markers.length;
  const saveDisabled = !canWrite || state.loading || state.saving || errorCount > 0 || Boolean(state.loadError);

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-slate-500 dark:text-slate-400" role="status">
          {state.loading
            ? "Loading /etc/network/interfaces…"
            : errorCount > 0
              ? `${String(errorCount)} syntax error${errorCount === 1 ? "" : "s"} — fix before saving`
              : "No syntax errors"}
        </p>
        <Tooltip content={saveDisabled ? disabledReason : undefined}>
          <span>
            <Button size="sm" disabled={saveDisabled} onClick={() => void actions.save()}>
              {state.saving ? "Saving…" : "Save as changeset"}
            </Button>
          </span>
        </Tooltip>
      </div>

      {state.hashConflict && (
        <div className="rounded-md border border-amber-400 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-600 dark:bg-amber-950/40 dark:text-amber-100">
          <p>
            The file on <strong>{node}</strong> changed on the server since you opened it. Reload it and reapply your
            edits before saving again.
          </p>
          <Button size="sm" variant="secondary" className="mt-2" onClick={() => void actions.reload()}>
            Reload file
          </Button>
        </div>
      )}

      {state.loadError && (
        <div className="rounded-md border border-red-400 bg-red-50 p-3 text-sm text-red-900 dark:border-red-600 dark:bg-red-950/40 dark:text-red-100">
          Could not load the file on {node}: {state.loadError}
        </div>
      )}

      {state.saveError && (
        <div className="rounded-md border border-red-400 bg-red-50 p-3 text-sm text-red-900 dark:border-red-600 dark:bg-red-950/40 dark:text-red-100">
          {state.saveError}
        </div>
      )}

      {state.blockingFindings.length > 0 && (
        <div className="rounded-md border border-red-400 bg-red-50 p-3 text-sm text-red-900 dark:border-red-600 dark:bg-red-950/40 dark:text-red-100">
          <p className="font-medium">
            Saved as a changeset draft, but it has {state.blockingFindings.length} blocking issue
            {state.blockingFindings.length === 1 ? "" : "s"} — open the drawer to review before applying:
          </p>
          <ul className="mt-1 list-disc pl-5">
            {state.blockingFindings.map((f, i) => (
              <li key={`${f.code}-${String(i)}`}>{f.message}</li>
            ))}
          </ul>
        </div>
      )}

      {state.savedChangesetId !== undefined && !state.hashConflict && !state.saveError && state.blockingFindings.length === 0 && (
        <div className="rounded-md border border-emerald-400 bg-emerald-50 p-3 text-sm text-emerald-900 dark:border-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-100">
          Saved to a changeset draft — review it in the drawer to validate and apply.
        </div>
      )}

      <Suspense
        fallback={
          <div className="flex h-[420px] items-center justify-center rounded-md border border-slate-300 text-sm text-slate-400 dark:border-slate-700">
            Loading editor…
          </div>
        }
      >
        <MonacoRawEditor value={state.content} onChange={actions.setContent} markers={state.markers} readOnly={!canWrite} />
      </Suspense>
    </div>
  );
}
