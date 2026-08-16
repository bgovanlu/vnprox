// `GET /gitsync/status` (T-2701) — the only route the git spec sync serves.
//
// There is deliberately no route that triggers a sync, applies its draft, or
// changes the remote (docs/api.md §"Git spec sync"), so this module has one
// read and no mutation. The daemon mounts the route whether or not `[gitsync]`
// is configured, because "it is off" is an answer an operator needs: an
// unconfigured sync answers `{"enabled": false}` with every other field
// omitted, which is a different state from a configured sync that is failing
// (`enabled: true` plus a non-empty `lastError`).
import { apiFetch } from "./client";

/** One `source: "gitsync"` finding as the status route inlines it
 * (internal/gitsync.Issue). `severity` is kept as the daemon's own string
 * rather than narrowed to the Severity union — nothing here needs to branch
 * on a closed set, and inventing one would be an unchecked cast. */
export interface GitSyncIssue {
  check: string;
  severity: string;
  detail: string;
}

/** `GET /gitsync/status` (docs/api.md's response shape).
 *
 * Every field but `enabled`, `requireSignedCommits` and `planOpCount` is
 * `omitempty` on the wire, so `undefined` here means "the daemon did not
 * report this", never "false"/"zero". Callers must keep that distinction:
 * a missing `lastSuccessAt` on an enabled sync is "no successful cycle yet",
 * and on a disabled one it is simply not applicable. */
export interface GitSyncStatus {
  enabled: boolean;
  /** The operator's remote URL, never carrying a credential. */
  remote?: string;
  ref?: string;
  path?: string;
  pollIntervalSeconds?: number;
  requireSignedCommits: boolean;
  lastFetchedSha?: string;
  /** Unix seconds — the last attempt, successful or not. */
  lastFetchAt?: number;
  /** Unix seconds — the last cycle that actually completed. */
  lastSuccessAt?: number;
  lastSigner?: string;
  /** The last cycle's failure, verbatim from the daemon. */
  lastError?: string;
  planOpCount: number;
  plan?: string[];
  /** Live entities the document does not declare — reported, never deleted. */
  notInSpec?: string[];
  openChangesetId?: string;
  openChangesetReason?: string;
  issues?: GitSyncIssue[];
}

/** The pull request an "adopt reality" (or a changeset propose) opened —
 * `internal/gitsync.Proposal`. Exactly one of `changesetId`/`findingId` is
 * ever non-empty; adoption sets `findingId`, because adopting moves the
 * document rather than the cluster. */
export interface SpecProposal {
  changesetId: string;
  findingId?: string;
  remote: string;
  branch: string;
  path: string;
  commitSha?: string;
  pullRequestId?: string;
  pullRequestUrl?: string;
  proposedBy?: string;
  proposedAt?: number;
  updatedAt?: number;
  /** False when an already-open request was brought up to date. */
  created: boolean;
}

/** GET /gitsync/status — `netRead`, no CSRF, no side effect. */
export function fetchGitSyncStatus(): Promise<GitSyncStatus> {
  return apiFetch<GitSyncStatus>("/gitsync/status");
}
