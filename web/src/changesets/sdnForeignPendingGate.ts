// SPDX-License-Identifier: Apache-2.0

// T-3101-followup-01's client-side echo of the server's foreign-SDN-pending
// "surface and confirm" gate — mirrors approvalGate.ts's own doc note
// exactly: this is UI convenience ONLY. internal/change's beginApply is the
// actual authorization decision, re-checked server-side (live, against PVE)
// on every apply attempt. sdnForeignPendingBlocksApply below exists so the
// Apply button can be disabled with an explanatory message and an
// "Acknowledge" action instead of letting the operator click Apply and get
// a surprise 422 sdn_foreign_pending_unacknowledged.
import type { SdnPendingEntry } from "../api/types";

/** One entry's own canonical, order-independent identity string — kind,
 * id, state, and every field value, exactly what internal/change's
 * sdnPendingEntryKeys (apply_sdn_foreign.go) hashes on the server side. */
function entryKey(e: SdnPendingEntry): string {
  return JSON.stringify([e.kind, e.id, e.state, e.fields ?? {}]);
}

/** Deterministic, order-independent key for a foreign-pending entry set —
 * used only as the value this screen remembers locally after a successful
 * acknowledgement; the server does the real, authoritative comparison
 * itself (isSDNForeignPendingCovered) from what it actually persisted. */
export function sdnPendingSetKey(entries: SdnPendingEntry[]): string {
  return entries.map(entryKey).sort().join("|");
}

/** True when the review screen should disable Apply until the operator
 * (re-)acknowledges: there IS foreign pending state right now, and it is
 * NOT fully covered — every current entry present, byte-for-byte, in what
 * `acknowledgedKey` represents — by the last successful acknowledgement in
 * this session. Deliberately NOT set-equality, mirroring the server's own
 * isSDNForeignPendingCovered exactly: an acknowledgement that covered MORE
 * than what's currently pending (some foreign edit was applied/reverted
 * since) still covers current just fine; only a NEW or CHANGED entry since
 * the ack must force re-acknowledgement. An absent `entries` (query still
 * loading, or errored) never blocks — exactly like blocksApply/
 * twoPersonBlocksApply's own "an absent/stale hint can only under-warn,
 * never under-enforce" rule: the server is what actually refuses the apply
 * if this under-warns. */
export function sdnForeignPendingBlocksApply(
  entries: SdnPendingEntry[] | undefined,
  acknowledgedKey: string | undefined,
): boolean {
  if (!entries || entries.length === 0) return false;
  const acked = new Set((acknowledgedKey ?? "").split("|"));
  return !entries.every((e) => acked.has(entryKey(e)));
}

/** A short, per-entry description for the review screen's listing —
 * "zone foreignz (new)". */
export function describeSdnPendingEntry(e: SdnPendingEntry): string {
  return `${e.kind} ${e.id} (${e.state})`;
}
