// SPDX-License-Identifier: Apache-2.0

// Pure helpers for T-208's raw editor op/finding wiring — no React, no
// network, directly Vitest-able. The op vocabulary and finding codes here
// mirror internal/change/op.go (OpIfaceRawReplace) and
// internal/change/validate_codes.go's "raw.*" class exactly.
import type { Finding, Op } from "../../api/types";

/** internal/inventory.Ref's "kind:node:id" encoding for the whole-file
 * target iface.raw.replace uses (a `node` Ref, not one entity — see
 * docs/data-model.md §3's iface.raw.replace row). */
export function rawNodeTarget(node: string): string {
  return `node:${node}:${node}`;
}

/** Builds the single-op changeset payload the raw editor's Save produces. */
export function buildRawReplaceOp(node: string, content: string, baseHash: string): Op {
  return {
    op: "iface.raw.replace",
    target: rawNodeTarget(node),
    params: { content, baseHash },
  };
}

/** Every finding attributed to node's raw.replace op itself (by ref) —
 * i.e. the raw.* class (parse error, hash conflict) that internal/change's
 * expandRawReplaceOps produces directly, as opposed to the findings its
 * *synthesized* delta ops (bridge.delete, etc.) attract, which are keyed
 * by the synthesized op's own ref instead (see errorFindings' doc
 * comment for why the editor's blocking-findings display doesn't filter
 * on ref at all). */
export function findingsForRawOp(findings: Finding[], node: string): Finding[] {
  const ref = rawNodeTarget(node);
  return findings.filter((f) => f.ref === ref);
}

/** True iff the changeset's findings show the hash-conflict guard fired
 * for node (internal/change's codeRawReplaceHashConflict) — the editor's
 * cue to prompt "reload the file" instead of treating this as an ordinary
 * validation error. */
export function hasHashConflict(findings: Finding[], node: string): boolean {
  return findingsForRawOp(findings, node).some((f) => f.code === "raw.hash_conflict");
}

/** Every error-severity finding on the changeset, regardless of ref — the
 * "reuse T-202 pipeline on the parsed entity delta" acceptance criterion
 * (AC2: "saving a file that deletes the management bridge -> interlock
 * error surfaces in the editor flow") means a raw edit's blocking findings
 * are usually attributed to a *synthesized* op's own ref (e.g.
 * `bridge:pve1:vmbr0` for a synthesized bridge.delete, not
 * `node:pve1:pve1` for the raw op itself) — see
 * internal/change/validate_raw.go's expandRawReplace. A raw-edit changeset
 * has (by construction, per schemaValidateRawReplaceExclusive) only this
 * one user-authored op, so every error finding on it is relevant to show
 * here regardless of which ref it landed on; excludes the hash-conflict
 * code, which gets its own dedicated reload-prompt UI instead of sitting
 * in this generic list. */
export function errorFindings(findings: Finding[]): Finding[] {
  return findings.filter((f) => f.severity === "error" && f.code !== "raw.hash_conflict");
}
