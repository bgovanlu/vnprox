// SPDX-License-Identifier: Apache-2.0

// Pure grouping logic split out of CommentsPanel.tsx so that component-only
// file keeps react-refresh's "only export components" invariant, and so
// this logic is independently unit-testable.
import type { ChangesetComment } from "../api/types";

/** Groups comments by the op they're attached to (or "" for a changeset-
 * level comment), preserving each group's first-appearance order among the
 * comments array (already oldest-first from the server). */
export function groupCommentsByOp(comments: ChangesetComment[]): Map<string, ChangesetComment[]> {
  const groups = new Map<string, ChangesetComment[]>();
  for (const c of comments) {
    const key = c.opId ?? "";
    const bucket = groups.get(key);
    if (bucket) bucket.push(c);
    else groups.set(key, [c]);
  }
  return groups;
}
