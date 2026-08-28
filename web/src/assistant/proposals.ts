// SPDX-License-Identifier: Apache-2.0

// T-2808 AC4: what the assistant may propose, and how that becomes a draft.
//
// The assistant NEVER accepts free-form ops from a model. It accepts a
// closed set of typed intents — the same shapes two of T-2705's typed MCP
// staging tools accept — and builds the op with the app's OWN existing op
// builders (changesets/opBuilders.ts), the same functions the entity
// editors call. So the op that reaches the change engine is one this
// codebase constructed, from parameters it validated, not JSON a model
// wrote.
//
// From there it is an ordinary draft: POST /changesets, the one write path
// (docs/api.md "Changesets — the only write path"), reviewed and applied by
// a human through the normal review surface. The assistant has no apply
// path, and — see boundary.test.ts — cannot even import one.
import { buildIfaceUpdateOp, buildIpamAllocCreateOp, type IfaceFormValues } from "../changesets/opBuilders";
import type { Op } from "../api/types";

/** The tag that marks a changeset the assistant staged (AC4). It goes in
 * the TITLE because a changeset's `origin` is server-assigned and the API
 * has no client-settable origin: an SPA-staged changeset is `origin: "ui"`,
 * and inventing an `origin: "assistant"` would be exactly the "new backend
 * capability" the card forbids. Recorded as a gap in the task report. */
export const ASSISTANT_TITLE_TAG = "[assistant]";

export type StagingProposal =
  | {
      kind: "iface.update";
      targetRef: string;
      mtu?: number;
      addresses?: string[];
      gateway?: string;
      autostart?: boolean;
    }
  | { kind: "ipam.alloc.create"; targetRef: string; cidr: string; hostname?: string };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function stringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const out: string[] = [];
  for (const entry of value) {
    if (typeof entry !== "string") {
      return undefined;
    }
    out.push(entry);
  }
  return out;
}

function parseProposal(value: unknown): StagingProposal | undefined {
  if (!isRecord(value) || typeof value.targetRef !== "string" || value.targetRef === "") {
    return undefined;
  }
  if (value.kind === "iface.update") {
    const addresses = stringArray(value.addresses);
    const proposal: StagingProposal = { kind: "iface.update", targetRef: value.targetRef };
    if (typeof value.mtu === "number" && Number.isFinite(value.mtu)) {
      proposal.mtu = value.mtu;
    }
    if (addresses !== undefined) {
      proposal.addresses = addresses;
    }
    if (typeof value.gateway === "string") {
      proposal.gateway = value.gateway;
    }
    if (typeof value.autostart === "boolean") {
      proposal.autostart = value.autostart;
    }
    // A proposal that changes nothing is not a proposal.
    const empty =
      proposal.mtu === undefined &&
      proposal.addresses === undefined &&
      proposal.gateway === undefined &&
      proposal.autostart === undefined;
    return empty ? undefined : proposal;
  }
  if (value.kind === "ipam.alloc.create" && typeof value.cidr === "string" && value.cidr !== "") {
    const proposal: StagingProposal = {
      kind: "ipam.alloc.create",
      targetRef: value.targetRef,
      cidr: value.cidr,
    };
    if (typeof value.hostname === "string" && value.hostname !== "") {
      proposal.hostname = value.hostname;
    }
    return proposal;
  }
  // Anything else — including an op type the assistant deliberately does
  // not propose — is dropped silently. The model cannot widen this set.
  return undefined;
}

/** Parses the reply's `proposals` field. Unknown kinds and malformed
 * entries are dropped rather than rejected wholesale: a good answer with
 * one bad proposal is still a good answer. */
export function parseProposals(value: unknown): StagingProposal[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const out: StagingProposal[] = [];
  for (const entry of value) {
    const parsed = parseProposal(entry);
    if (parsed !== undefined) {
      out.push(parsed);
    }
  }
  return out;
}

/** A zeroed "current values" baseline for buildIfaceUpdateOp's diffing
 * contract: every field the proposal did not name is left equal to its
 * baseline, so the built op carries ONLY the named fields. */
const IFACE_BASELINE: IfaceFormValues = { mtu: 0, comments: "", addresses: [], gateway: "", autostart: false };

/** Turns a proposal into the op the change engine will see, using the
 * app's own builders. */
export function proposalToOp(proposal: StagingProposal): Op {
  if (proposal.kind === "iface.update") {
    const form: IfaceFormValues = {
      ...IFACE_BASELINE,
      ...(proposal.mtu === undefined ? {} : { mtu: proposal.mtu }),
      ...(proposal.addresses === undefined ? {} : { addresses: proposal.addresses }),
      ...(proposal.gateway === undefined ? {} : { gateway: proposal.gateway }),
      ...(proposal.autostart === undefined ? {} : { autostart: proposal.autostart }),
    };
    return buildIfaceUpdateOp(proposal.targetRef, IFACE_BASELINE, form);
  }
  return buildIpamAllocCreateOp(proposal.targetRef, proposal.cidr, proposal.hostname);
}

/** One-line human summary, used for the draft's title and in the panel. */
export function proposalSummary(proposal: StagingProposal): string {
  if (proposal.kind === "iface.update") {
    const parts: string[] = [];
    if (proposal.mtu !== undefined) parts.push(`MTU ${String(proposal.mtu)}`);
    if (proposal.addresses !== undefined) parts.push(`addresses ${proposal.addresses.join(", ")}`);
    if (proposal.gateway !== undefined) parts.push(`gateway ${proposal.gateway}`);
    if (proposal.autostart !== undefined) parts.push(`autostart ${String(proposal.autostart)}`);
    return `update ${proposal.targetRef}: ${parts.join("; ")}`;
  }
  return `reserve ${proposal.cidr} in ${proposal.targetRef}`;
}

/** The title a staged draft gets: tagged, then the proposal's own summary.
 * The tag is a prefix so it is visible in every list that truncates. */
export function assistantDraftTitle(proposals: StagingProposal[]): string {
  const first = proposals[0];
  const summary = first === undefined ? "proposal" : proposalSummary(first);
  const more = proposals.length > 1 ? ` (+${String(proposals.length - 1)} more)` : "";
  return `${ASSISTANT_TITLE_TAG} ${summary}${more}`;
}
