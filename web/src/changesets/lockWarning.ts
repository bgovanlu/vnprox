// SPDX-License-Identifier: Apache-2.0

// T-2805 — the UI half of advisory entity locks.
//
// The card's rule, twice stated: **a lock never prevents an emergency change;
// it prevents an accidental one.** Nothing in this module (or the component
// that renders it) may disable an action, hide a button, or gate a flow. It
// produces a *sentence* — who else has this open, and what proceeding means —
// and that is the entire mechanism.
//
// The two facts it has to state honestly:
//
//   - The changeset was ALREADY staged. `locks.held` arrives on the response
//     to a create/update that succeeded, so the copy must never read like a
//     rejection the operator has to clear.
//   - The holder's name may be absent. `holder` is omitted for a session
//     without the `audit` capability (docs/api.md), so every string here has
//     to work without it rather than rendering "held by undefined".
//
// Framework-free and directly Vitest-able, the same convention
// revertCoverage.ts and planPreview.ts already follow.
import type { ChangesetLocks, EntityLock } from "../api/types";

/** The person shown as a lock's holder, or a truthful stand-in when this
 * session may not be told. Never "undefined", never an empty string in the
 * middle of a sentence. */
export function holderLabel(lock: EntityLock): string {
  return lock.holder && lock.holder.length > 0 ? lock.holder : "another operator";
}

/** A short entity label for the ref triplet `kind:node:id` — the id, scoped
 * by node when there is one. Falls back to the raw ref rather than throwing
 * on anything unexpected: a warning that crashes the drawer would be worse
 * than a slightly ugly one. */
export function refLabel(ref: string): string {
  const parts = ref.split(":");
  if (parts.length < 3) return ref;
  const node = parts[1] ?? "";
  const id = parts.slice(2).join(":");
  if (id === "") return ref;
  return node === "" ? id : `${id} on ${node}`;
}

export interface LockNotice {
  /** Whether to render anything at all. False for an uncontended staging. */
  show: boolean;
  /** "warning" — another operator's claim was left alone; "override" — this
   * request took one over. Purely a presentation distinction; neither
   * blocks anything. */
  kind: "warning" | "override";
  heading: string;
  /** One line per contended entity, naming the holder. */
  lines: string[];
  /** What the operator can do next, stated so that "proceed" reads as a
   * deliberate act rather than the default. Empty for an override notice —
   * the deliberate act already happened. */
  action: string;
}

const NO_NOTICE: LockNotice = { show: false, kind: "warning", heading: "", lines: [], action: "" };

/**
 * Turns a staging response's `locks` object into the notice to render.
 *
 * An override notice wins when both are present: having just taken someone
 * else's claim is the more important thing to tell the operator, and the
 * remaining `held` entries (if any) get their own line in the same block via
 * a second call on the next staging round-trip.
 */
export function lockNotice(locks: ChangesetLocks | undefined): LockNotice {
  const overridden = locks?.overridden ?? [];
  const held = locks?.held ?? [];

  if (overridden.length > 0) {
    return {
      show: true,
      kind: "override",
      heading:
        overridden.length === 1
          ? "You took over another operator's draft lock"
          : `You took over ${String(overridden.length)} draft locks`,
      lines: overridden.map((l) => `${refLabel(l.ref)} — was held by ${holderLabel(l)}`),
      action: "This override was recorded in the audit log.",
    };
  }

  if (held.length > 0) {
    return {
      show: true,
      kind: "warning",
      heading:
        held.length === 1
          ? "Someone else already has a draft open on this entity"
          : `Someone else already has a draft open on ${String(held.length)} of these entities`,
      lines: held.map((l) => `${refLabel(l.ref)} — held by ${holderLabel(l)}`),
      // Deliberately says what the operator's change already is (staged) and
      // what it is not (blocked), because a warning that implies a block
      // trains people to ignore warnings.
      action:
        "Your changes are staged either way — this does not block anything. " +
        "Save again with “take over the lock” if you mean to proceed regardless.",
    };
  }

  return NO_NOTICE;
}

/** Every distinct holder named in a notice's underlying locks, for a compact
 * "also being edited by …" summary. Callers that cannot see identities get an
 * empty list, which is the correct thing to render nothing from. */
export function lockHolders(locks: ChangesetLocks | undefined): string[] {
  const all = [...(locks?.held ?? []), ...(locks?.overridden ?? [])];
  const seen = new Set<string>();
  for (const l of all) {
    if (l.holder) seen.add(l.holder);
  }
  return [...seen].sort();
}

/** How many other people are on a presence scope, given the raw count and
 * whether this session is itself counted. The server counts every viewer
 * including the caller, so the UI's "N others" has to subtract one — showing
 * yourself as a colleague is the bug this exists to prevent. */
export function othersPresent(count: number, includesSelf: boolean): number {
  const n = includesSelf ? count - 1 : count;
  return n > 0 ? n : 0;
}

/** The "who else is here" sentence, degrading gracefully when identities are
 * withheld: it still says HOW MANY, because a count is not an identity. */
export function presenceSentence(others: number, names: readonly string[]): string {
  if (others <= 0) return "";
  const known = names.filter((n) => n.length > 0);
  if (known.length === 0) {
    return others === 1 ? "1 other person is viewing this." : `${String(others)} other people are viewing this.`;
  }
  // `noUncheckedIndexedAccess` makes every index access `string | undefined`;
  // the lengths are checked above, so the fallbacks are unreachable rather
  // than defensive — and an unreachable "" beats a non-null assertion.
  const first = known[0] ?? "";
  if (known.length === 1) return `${first} is also viewing this.`;
  const last = known[known.length - 1] ?? "";
  if (known.length === 2) return `${first} and ${last} are also viewing this.`;
  return `${known.slice(0, -1).join(", ")} and ${last} are also viewing this.`;
}
