// The `GET /gitsync/status` state machine, kept out of the component so the
// one property that matters here is directly testable: **an unknown state is
// never rendered as a definite one**.
//
// Four states an operator has to be able to tell apart, and the copy for each
// is deliberately different:
//
//   not-configured  no `[gitsync]` section at all. Nothing is fetched, no
//                   endpoint is contacted, and there is nothing to fix.
//   failing         configured, and the last cycle failed. The daemon's own
//                   `lastError` is the message — never a generic one.
//   healthy         configured, and the last cycle completed.
//   unreadable      we could not ask (the daemon is down, or the read was
//                   refused). This is NOT "gitsync is off": conflating them
//                   would tell an operator their GitOps sync is disabled at
//                   exactly the moment they cannot see that it is broken.
import type { GitSyncStatus } from "../api/gitsync";

export type GitSyncState =
  | { kind: "loading" }
  | { kind: "unreadable"; message: string }
  | { kind: "not-configured" }
  | { kind: "failing"; status: GitSyncStatus; message: string }
  | { kind: "healthy"; status: GitSyncStatus };

/** Classifies one status read. `error` takes precedence over a stale cached
 * `status`, because a status we could not refresh is not evidence of the
 * current state. */
export function gitSyncState(
  status: GitSyncStatus | undefined,
  isLoading: boolean,
  error: unknown,
): GitSyncState {
  if (error !== null && error !== undefined) {
    return {
      kind: "unreadable",
      message: error instanceof Error && error.message !== "" ? error.message : "the status read failed",
    };
  }
  if (isLoading || status === undefined) {
    return { kind: "loading" };
  }
  if (!status.enabled) {
    return { kind: "not-configured" };
  }
  const lastError = status.lastError ?? "";
  if (lastError !== "") {
    return { kind: "failing", status, message: lastError };
  }
  return { kind: "healthy", status };
}

/** Whether adopting live state into the document could possibly work.
 *
 * `POST /drift/{id}/adopt-reality` needs a write-capable spec repository
 * (`[gitsync] push_token_file`) and answers `501 not_implemented` without one.
 * No route reports that credential's presence — `GET /gitsync/status` does not
 * carry it — so the only thing this can decide up front is the one case where
 * the answer is certain: with no `[gitsync]` section there is no push
 * credential either, so the action is unavailable. Every other case is
 * `"unknown"`, and the daemon's own 501 is what settles it. */
export function adoptAvailability(state: GitSyncState): "unavailable" | "unknown" {
  return state.kind === "not-configured" ? "unavailable" : "unknown";
}

/** Whether a spec position exists at all, which decides whether "no
 * disagreements" means agreement or means nothing was compared.
 *
 * A spec can come from either source — the git sync (T-2701) or the pin
 * (T-1102) — so it takes both reads to conclude there is none, and either read
 * failing leaves the answer `"unknown"`. This is the check that keeps an
 * un-asked question from rendering as a healthy one. */
export function specPresence(
  gitSync: GitSyncState,
  pin: { pinned: boolean } | undefined,
  pinError: unknown,
): "present" | "absent" | "unknown" {
  if (pin?.pinned === true) {
    return "present";
  }
  if (gitSync.kind === "healthy" || gitSync.kind === "failing") {
    return "present";
  }
  const pinKnown = pin !== undefined && (pinError === null || pinError === undefined);
  if (pinKnown && gitSync.kind === "not-configured") {
    return "absent";
  }
  return "unknown";
}

/** A unix-seconds instant as local text, or a caller-supplied phrase when the
 * daemon omitted the field. Never renders an omitted timestamp as the epoch —
 * "1 Jan 1970" is the classic way an unknown becomes a definite. */
export function instantLabel(seconds: number | undefined, absent: string): string {
  if (seconds === undefined || seconds === 0) {
    return absent;
  }
  return new Date(seconds * 1000).toLocaleString();
}
