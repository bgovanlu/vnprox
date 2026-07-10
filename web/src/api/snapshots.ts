// Snapshots / time-machine API calls (docs/api.md "Snapshots / time
// machine", T-206). Types are defined here rather than in types.ts to keep
// this task's edits to shared files minimal while T-207 works on the
// changeset types concurrently.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";

/** One snapshot in the paginated list (docs/api.md snapshot shape). */
export interface SnapshotSummary {
  id: string;
  kind: "pre" | "post" | "manual" | "scheduled";
  changesetId?: string;
  note?: string;
  takenAt: number;
  nodes: string[];
}

/** One captured file's identity within a snapshot (no content — that lives
 * in the server's deduplicated blob store; fetch diffs instead). */
export interface SnapshotFileMeta {
  node: string;
  path: string;
  sha256: string;
}

/** GET /snapshots/{id}: the summary plus the captured file list. */
export interface SnapshotDetail extends SnapshotSummary {
  files: SnapshotFileMeta[];
}

/** Paginated list envelope (docs/api.md: `{items, nextCursor?}`). */
export interface SnapshotListResponse {
  items: SnapshotSummary[];
  nextCursor?: string;
}

/** One file's diff in a GET /snapshots/diff response. `unified` is empty
 * when `changed` is false. */
export interface SnapshotFileDiff {
  node: string;
  path: string;
  unified: string;
  changed: boolean;
}

export interface SnapshotDiffResponse {
  files: SnapshotFileDiff[];
}

/** The subset of the changeset response POST /snapshots/{id}/restore
 * returns that this feature needs (the full changeset type is T-207's;
 * declaring only what History consumes avoids a shared-file conflict). */
export interface RestoreDraftResponse {
  id: string;
  title: string;
  status: string;
}

/** GET /snapshots — one page, newest first. Pass the previous response's
 * nextCursor to fetch the next page. */
export function fetchSnapshots(cursor?: string, limit = 50): Promise<SnapshotListResponse> {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  if (cursor) {
    params.set("cursor", cursor);
  }
  return apiFetch<SnapshotListResponse>(`/snapshots?${params.toString()}`);
}

/** GET /snapshots/{id} — metadata + file list. */
export function fetchSnapshotDetail(id: string): Promise<SnapshotDetail> {
  return apiFetch<SnapshotDetail>(`/snapshots/${encodeURIComponent(id)}`);
}

/** GET /snapshots/diff — unified diffs between two snapshots, or between a
 * snapshot and the current live state (`to === "live"`). */
export function fetchSnapshotDiff(from: string, to: string): Promise<SnapshotDiffResponse> {
  const params = new URLSearchParams({ from, to });
  return apiFetch<SnapshotDiffResponse>(`/snapshots/diff?${params.toString()}`);
}

/** POST /snapshots — capture a manual snapshot of every node now. */
export function createSnapshot(note: string): Promise<SnapshotSummary> {
  return apiFetch<SnapshotSummary>("/snapshots", {
    json: { note },
    csrfToken: readCsrfCookie(),
  });
}

/** POST /snapshots/{id}/restore — creates a draft changeset that would
 * restore this snapshot's state; it then goes through the normal
 * review/validate/apply flow like any other draft. */
export function restoreSnapshot(id: string): Promise<RestoreDraftResponse> {
  return apiFetch<RestoreDraftResponse>(`/snapshots/${encodeURIComponent(id)}/restore`, {
    json: {},
    csrfToken: readCsrfCookie(),
  });
}
