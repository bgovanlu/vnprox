// SPDX-License-Identifier: Apache-2.0

// T-2605's post-apply topology preview: `GET /changesets/{id}/preview`.
//
// The blast radius (T-2404) answers "what breaks" and the diff answers "what
// fields change". This answers the question an operator actually forms before
// clicking apply: what the map will look like afterwards.
//
// Two things about this contract are worth stating here rather than leaving to
// the server: the projection is BEST-EFFORT (`bestEffort` is always true), and
// anything it could not project is listed BY NAME with a reason in
// `unprojectable` rather than silently missing. A UI that renders the projected
// map without surfacing that list would turn a disclosed gap back into a hidden
// one.
import { apiFetch } from "./client";
import type { TopologyResponse } from "./types";

export type PreviewChangeKind = "added" | "removed" | "modified";

export interface PreviewFieldChange {
  field: string;
  before: string;
  after: string;
}

/** One entity's difference between the live map and the projected one. */
export interface PreviewChange {
  ref: string;
  kind: string;
  node?: string;
  name?: string;
  change: PreviewChangeKind;
  fields: PreviewFieldChange[];
}

/** One op the projection could not express, and why. `reason` is never empty:
 * an op listed without one reads as an unexplained gap. */
export interface UnprojectableOp {
  opId?: string;
  op: string;
  target?: string;
  reason: string;
}

export interface ChangesetPreviewResponse {
  changesetId: string;
  /** The projected map, in the same shape `GET /topology` returns — so it
   * renders through the renderer the map already has. */
  topology: TopologyResponse;
  changes: PreviewChange[];
  unprojectable: UnprojectableOp[];
  bestEffort: boolean;
  generatedAt: number;
}

/** GET /changesets/{id}/preview — the cluster map as it would be with this
 * changeset applied. Read-only: the server touches neither its store nor PVE. */
export function fetchChangesetPreview(changesetId: string): Promise<ChangesetPreviewResponse> {
  return apiFetch<ChangesetPreviewResponse>(`/changesets/${encodeURIComponent(changesetId)}/preview`);
}
