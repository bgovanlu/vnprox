// History — the configuration time machine (T-206, docs/user-guide.md §3:
// "History → Snapshots lets you diff any two points and restore any of
// them — restores go through the same review flow").
import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createSnapshot,
  fetchSnapshotDiff,
  fetchSnapshots,
  restoreSnapshot,
  type SnapshotDiffResponse,
  type SnapshotListResponse,
  type SnapshotSummary,
} from "../api/snapshots";
import { useSession } from "../api/useSession";
import { ApiError } from "../api/client";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { Tooltip } from "../components/Tooltip";
import { useToast } from "../components/Toast";
import { DiffView } from "./DiffView";
import { TopologyDiffPanel } from "./TopologyDiffPanel";
import { groupSnapshots, kindLabel } from "./timeline";

const SNAPSHOTS_QUERY_KEY = ["snapshots"] as const;
const PAGE_SIZE = 50;

function formatTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

/** The two endpoints of the diff being viewed. `to` may be the special
 * "live" token (docs/api.md: `to=live`). */
interface DiffSelection {
  from?: string;
  to?: string;
}

export function HistoryPage() {
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const { data: session } = useSession();
  const [selection, setSelection] = useState<DiffSelection>({});
  const [note, setNote] = useState("");
  // Which reading of the same two points is shown: the raw file diff this
  // page has always had, or T-2704's entity-level topology diff (which also
  // answers "did vnprox do this?"). A per-session toggle, not persisted.
  const [diffMode, setDiffMode] = useState<"files" | "topology">("topology");
  const [restoreTarget, setRestoreTarget] = useState<SnapshotSummary | undefined>(undefined);

  // T-605 read-only sweep finding: neither "Take snapshot" (POST
  // /snapshots) nor "Create restore draft" (POST /snapshots/{id}/restore)
  // had any capability gating at all — this whole page's write surface
  // predates the capsForNode/hasAnyCap convention every other write
  // affordance in the app uses. Both actions are cluster-wide (a manual
  // snapshot captures whichever nodes' files are relevant; a restore draft
  // can span nodes too), so gated the same way the onboarding walkthrough/
  // blueprints/drift-fix cluster-wide writes are: hasAnyCap(netWrite).
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  const snapshotsQuery = useInfiniteQuery<SnapshotListResponse>({
    queryKey: SNAPSHOTS_QUERY_KEY,
    queryFn: ({ pageParam }) =>
      fetchSnapshots(typeof pageParam === "string" && pageParam !== "" ? pageParam : undefined, PAGE_SIZE),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });

  const snapshots = useMemo(
    () => (snapshotsQuery.data?.pages ?? []).flatMap((p) => p.items),
    [snapshotsQuery.data],
  );
  const groups = useMemo(() => groupSnapshots(snapshots), [snapshots]);

  const diffEnabled = Boolean(selection.from && selection.to);
  const diffQuery = useQuery<SnapshotDiffResponse>({
    queryKey: ["snapshot-diff", selection.from ?? "", selection.to ?? ""],
    queryFn: () => fetchSnapshotDiff(selection.from ?? "", selection.to ?? ""),
    // Only the Files view consumes it — the Topology view has its own query,
    // and fetching both would double every diff request for no reader.
    enabled: diffEnabled && diffMode === "files",
  });

  const createMutation = useMutation({
    mutationFn: (n: string) => createSnapshot(n),
    onSuccess: (created) => {
      setNote("");
      toast({ title: "Snapshot captured", description: `Snapshot ${created.id} saved.`, variant: "success" });
      void queryClient.invalidateQueries({ queryKey: SNAPSHOTS_QUERY_KEY });
    },
    onError: (err) => {
      toast({
        title: "Snapshot failed",
        description: err instanceof ApiError ? err.message : "unexpected error",
        variant: "error",
      });
    },
  });

  const restoreMutation = useMutation({
    mutationFn: (id: string) => restoreSnapshot(id),
    onSuccess: (draft) => {
      setRestoreTarget(undefined);
      toast({
        title: "Restore draft created",
        description: `Draft "${draft.title}" (${draft.id}) is ready — review and apply it from the changeset drawer.`,
        variant: "success",
        durationMs: 8000,
      });
    },
    onError: (err) => {
      toast({
        title: "Restore failed",
        description: err instanceof ApiError ? err.message : "unexpected error",
        variant: "error",
      });
    },
  });

  const selectFrom = (id: string) => {
    setSelection((prev) => ({ ...prev, from: id }));
  };
  const selectTo = (id: string) => {
    setSelection((prev) => ({ ...prev, to: id }));
  };

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">History</h1>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!canWrite) return;
            createMutation.mutate(note);
          }}
        >
          <input
            type="text"
            value={note}
            onChange={(e) => {
              setNote(e.target.value);
            }}
            placeholder="Note (optional)"
            aria-label="Snapshot note"
            className="h-9 w-56 rounded-md border border-slate-300 bg-white px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
          <Tooltip content={writeDisabledReason}>
            <span>
              <Button type="submit" variant="primary" size="md" disabled={createMutation.isPending || !canWrite}>
                {createMutation.isPending ? "Capturing…" : "Take snapshot"}
              </Button>
            </span>
          </Tooltip>
        </form>
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Timeline */}
        <section className="flex min-h-0 flex-col gap-2 overflow-y-auto pr-1" aria-label="Snapshot timeline">
          {snapshotsQuery.isLoading ? (
            <p className="text-sm text-slate-500">Loading snapshots…</p>
          ) : snapshotsQuery.isError ? (
            <EmptyState
              title="Could not load snapshots"
              description={
                snapshotsQuery.error instanceof ApiError ? snapshotsQuery.error.message : "unexpected error"
              }
            />
          ) : groups.length === 0 ? (
            <EmptyState
              title="No snapshots yet"
              description="Snapshots are captured automatically before and after every applied changeset. You can also take one manually."
            />
          ) : (
            <>
              {groups.map((group) => (
                <article
                  key={group.key}
                  className="rounded-lg border border-slate-200 p-3 dark:border-slate-800"
                >
                  <header className="mb-2 flex items-center justify-between gap-2">
                    <span className="text-sm font-medium text-slate-800 dark:text-slate-100">
                      {group.changesetId ? `Changeset ${group.changesetId}` : "Standalone snapshot"}
                    </span>
                    <time className="text-xs text-slate-500">{formatTime(group.at)}</time>
                  </header>
                  <ul className="flex flex-col gap-1.5">
                    {group.snapshots.map((snap) => (
                      <li
                        key={snap.id}
                        className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-slate-50 px-2 py-1.5 dark:bg-slate-900/60"
                      >
                        <div className="flex min-w-0 flex-col">
                          <span className="text-sm text-slate-700 dark:text-slate-200">
                            {kindLabel(snap.kind)}
                            {snap.note ? ` — ${snap.note}` : ""}
                          </span>
                          <span className="truncate text-xs text-slate-500">
                            {snap.id} · {formatTime(snap.takenAt)} · {snap.nodes.join(", ")}
                          </span>
                        </div>
                        <div className="flex items-center gap-1">
                          <Button
                            size="sm"
                            variant={selection.from === snap.id ? "primary" : "ghost"}
                            onClick={() => {
                              selectFrom(snap.id);
                            }}
                          >
                            From
                          </Button>
                          <Button
                            size="sm"
                            variant={selection.to === snap.id ? "primary" : "ghost"}
                            onClick={() => {
                              selectTo(snap.id);
                            }}
                          >
                            To
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => {
                              setSelection({ from: snap.id, to: "live" });
                            }}
                          >
                            vs live
                          </Button>
                          <Tooltip content={writeDisabledReason}>
                            <span>
                              <Button
                                size="sm"
                                variant="secondary"
                                disabled={!canWrite}
                                onClick={() => {
                                  setRestoreTarget(snap);
                                }}
                              >
                                Restore…
                              </Button>
                            </span>
                          </Tooltip>
                        </div>
                      </li>
                    ))}
                  </ul>
                </article>
              ))}
              {snapshotsQuery.hasNextPage ? (
                <Button
                  variant="secondary"
                  onClick={() => {
                    void snapshotsQuery.fetchNextPage();
                  }}
                  disabled={snapshotsQuery.isFetchingNextPage}
                >
                  {snapshotsQuery.isFetchingNextPage ? "Loading…" : "Load older snapshots"}
                </Button>
              ) : null}
            </>
          )}
        </section>

        {/* Diff panel */}
        <section className="flex min-h-0 flex-col gap-2 overflow-y-auto" aria-label="Snapshot diff">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">Diff</h2>
              <div className="flex items-center gap-1" role="group" aria-label="Diff view">
                <Button
                  size="sm"
                  variant={diffMode === "topology" ? "primary" : "ghost"}
                  onClick={() => {
                    setDiffMode("topology");
                  }}
                >
                  Topology
                </Button>
                <Button
                  size="sm"
                  variant={diffMode === "files" ? "primary" : "ghost"}
                  onClick={() => {
                    setDiffMode("files");
                  }}
                >
                  Files
                </Button>
              </div>
            </div>
            <span className="text-xs text-slate-500">
              {selection.from ?? "(pick From)"} → {selection.to ?? "(pick To or vs live)"}
            </span>
          </div>
          {diffMode === "topology" ? (
            <TopologyDiffPanel from={selection.from ?? ""} to={selection.to ?? ""} />
          ) : !diffEnabled ? (
            <EmptyState
              title="Pick two points to compare"
              description='Choose a "From" and a "To" snapshot on the timeline, or use "vs live" to compare a snapshot with the current configuration.'
            />
          ) : diffQuery.isLoading ? (
            <p className="text-sm text-slate-500">Computing diff…</p>
          ) : diffQuery.isError ? (
            <EmptyState
              title="Diff failed"
              description={diffQuery.error instanceof ApiError ? diffQuery.error.message : "unexpected error"}
            />
          ) : (
            (diffQuery.data?.files ?? []).map((file) => (
              <div key={`${file.node}:${file.path}`} className="flex flex-col gap-1">
                <h3 className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  {file.node} · {file.path}
                  {file.changed ? "" : " (unchanged)"}
                </h3>
                {file.changed ? <DiffView unified={file.unified} /> : null}
              </div>
            ))
          )}
        </section>
      </div>

      {/* Restore confirmation */}
      <Dialog
        open={restoreTarget !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            setRestoreTarget(undefined);
          }
        }}
      >
        <DialogContent>
          <DialogTitle>Restore snapshot?</DialogTitle>
          <DialogDescription>
            This creates a <strong>draft changeset</strong> that would bring{" "}
            {restoreTarget?.nodes.join(", ") ?? "the captured nodes"} back to the state captured at{" "}
            {restoreTarget ? formatTime(restoreTarget.takenAt) : ""}. Nothing changes until you review
            and apply that draft — it goes through the same validation, diff, and commit-confirm flow
            as any other change.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild>
              <Button variant="ghost">Cancel</Button>
            </DialogClose>
            <Tooltip content={writeDisabledReason}>
              <span>
                <Button
                  variant="primary"
                  disabled={restoreMutation.isPending || !canWrite}
                  onClick={() => {
                    if (restoreTarget && canWrite) {
                      restoreMutation.mutate(restoreTarget.id);
                    }
                  }}
                >
                  {restoreMutation.isPending ? "Creating draft…" : "Create restore draft"}
                </Button>
              </span>
            </Tooltip>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
