// Microsegmentation planner API calls (docs/api.md's Microsegmentation
// section, T-1602's `POST /microseg/propose` + `POST /microseg/dry-run`).
// Both routes are read-only synthesis — they READ a guest's observed flows
// and firewall state and COMPUTE a proposal; nothing here mutates or stages
// (the proposal's own `stagedOps` are handed into the ordinary
// ChangesetDrawer by the review UI, T-1603). Kept in the shared api/ layer
// per docs/development.md's "no fetch in components" rule, the same as
// every other feature's client module.
import { apiFetch } from "./client";
import type { MicrosegDryRunReport, MicrosegProposal } from "./types";

/** POST /microseg/propose — the minimal covering-set firewall policy for
 * `guestRef` plus the changeset ops that would stage it. Rejects
 * (ApiError) with `validation_failed` for a malformed ref and `not_found`
 * for a well-formed ref with no observable flows/guest. */
export function proposeMicroseg(guestRef: string): Promise<MicrosegProposal> {
  return apiFetch<MicrosegProposal>("/microseg/propose", { json: { guestRef } });
}

/** POST /microseg/dry-run — replays a flow corpus against the proposal for
 * `guestRef` and returns the four-bucket honest report. `heldOut: true`
 * replays a trailing held-out window (excluded from the training window the
 * proposal was learned over) rather than the training corpus itself. */
export function dryRunMicroseg(guestRef: string, heldOut = false): Promise<MicrosegDryRunReport> {
  return apiFetch<MicrosegDryRunReport>("/microseg/dry-run", { json: { guestRef, heldOut } });
}
