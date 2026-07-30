// T-1805 / roadmap-proven D1 — the UI half of the apply-time revert ticket.
//
// The product's central promise is "if the change locks you out, it reverts
// itself". For node-file changes the daemon can keep that promise on its own:
// it writes /etc/network/interfaces and runs ifreload as root, with no user
// credential involved. PVE **firewall and SDN** writes are different — they go
// out under the *user's* own PVE ticket — so reverting them with no session
// alive depends on the ticket vnprox sealed when the apply started, and that
// ticket has its own ~2h lifetime.
//
// This module is the pure logic behind two statements the UI must make:
//
//   - **Before apply** (the review screen): does this changeset contain ops
//     whose unattended revert depends on that sealed ticket, and — given the
//     confirm window the operator is about to choose — what exactly is
//     promised? No changeset may imply a safety net it does not have.
//   - **During the confirm window** (the countdown banner): the server's own
//     `unattendedRevert` report, which knows the real ticket expiry the client
//     cannot see.
//
// Framework-free and directly Vitest-able, the same convention planPreview.ts
// and scopeSummary.ts already follow.
import type { Changeset, Op, UnattendedRevert } from "../api/types";

/** Op families whose revert requires the user's PVE ticket — mirrors
 * internal/change's Plan.needsRevertTicket (hasFw() || hasSDN()) exactly.
 * `fw.*` are the firewall ops (T-502); `sdn.*` covers both the cluster-scope
 * stage ops and the trailing `sdn.apply` (T-402). */
export function opNeedsRevertTicket(op: Op): boolean {
  return op.op.startsWith("fw.") || op.op.startsWith("sdn.");
}

/** Does this changeset contain any op whose unattended revert depends on the
 * sealed PVE ticket? */
export function changesetNeedsRevertTicket(ops: readonly Op[]): boolean {
  return ops.some(opNeedsRevertTicket);
}

/** The distinct ticket-scoped op families present, for naming them plainly in
 * the copy rather than saying "some ops". */
export function revertTicketOpFamilies(ops: readonly Op[]): string[] {
  const out: string[] = [];
  if (ops.some((o) => o.op.startsWith("fw."))) out.push("firewall");
  if (ops.some((o) => o.op.startsWith("sdn."))) out.push("SDN");
  return out;
}

export interface PreApplyRevertNotice {
  /** Whether the review screen should show the notice at all. */
  show: boolean;
  /** Plain-language heading. */
  heading: string;
  /** The body: what will and will not revert itself, and for how long. */
  body: string;
}

/**
 * The review screen's pre-apply statement. The client cannot know the exact
 * remaining life of the operator's PVE ticket (only the server sees it), so
 * this deliberately promises nothing it cannot back: it names which parts of
 * the changeset depend on the sealed ticket, states the window they are
 * covered for, and says plainly what happens if the PVE session has already
 * expired. The precise coverage bound arrives in the apply response
 * (`unattendedRevert`) and is rendered by the countdown banner.
 */
export function preApplyRevertNotice(
  ops: readonly Op[],
  confirmTimeoutSec: number,
): PreApplyRevertNotice {
  if (!changesetNeedsRevertTicket(ops)) {
    return { show: false, heading: "", body: "" };
  }
  const families = revertTicketOpFamilies(ops);
  const familyText = families.join(" and ");
  const hasNodeFileOps = ops.some((o) => !opNeedsRevertTicket(o));
  const windowText = `${String(confirmTimeoutSec)} second${confirmTimeoutSec === 1 ? "" : "s"}`;

  const parts: string[] = [];
  parts.push(
    `Proxmox applies ${familyText} changes under your own login, not vnprox's. ` +
      `So that this changeset can still undo itself if it cuts you off, vnprox keeps an encrypted copy of ` +
      `your Proxmox session for the ${windowText} of the confirm window, uses it only to undo this one ` +
      `changeset, and deletes it the moment you confirm or it rolls back.`,
  );
  parts.push(
    `If your Proxmox login expires before the confirm window closes (a Proxmox session lasts about 2 hours), ` +
      `the ${familyText} part of this change will no longer undo itself automatically after that point — ` +
      `you would have to undo it by hand. vnprox tells you the exact cut-off as soon as you apply.`,
  );
  if (hasNodeFileOps) {
    parts.push(
      `The per-node interface changes in this changeset are not affected: those always undo themselves, ` +
        `because vnprox performs them itself and needs no login to reverse them.`,
    );
  }

  return {
    show: true,
    heading: `Undoing ${familyText} changes needs your Proxmox login`,
    body: parts.join(" "),
  };
}

export interface RevertCoverageBanner {
  /** Whether to say anything at all (false for a changeset whose revert needs
   * no ticket, and for an older response with no report). */
  show: boolean;
  /** "ok" — fully covered; "partial" — covered until a point inside the
   * window; "none" — the ticket-scoped portion will not self-revert. */
  tone: "ok" | "partial" | "none";
  text: string;
}

/**
 * The countdown banner's statement, from the server's own report. Reads
 * `unattendedRevert` verbatim rather than re-deriving anything: the server is
 * the only party that knows whether a ticket was actually sealed and when it
 * expires.
 *
 * A changeset with no report (an older server, or a changeset outside its
 * confirm window) shows nothing — silence is correct where the client has no
 * basis for a claim, and a wrong reassurance here is worse than none.
 */
export function revertCoverageBanner(
  changeset: Pick<Changeset, "unattendedRevert" | "confirmDeadline">,
  nowMs: number,
): RevertCoverageBanner {
  const report: UnattendedRevert | undefined = changeset.unattendedRevert;
  if (!report?.required) {
    return { show: false, tone: "ok", text: "" };
  }
  if (!report.available) {
    return {
      show: true,
      tone: "none",
      text:
        "Heads up: the firewall/SDN part of this change will NOT undo itself if the window runs out — " +
        (report.reason ?? "no Proxmox session credential was available when it was applied.") +
        " Undo it by hand if you lose access.",
    };
  }
  if (!report.fullWindow && report.coversUntil !== undefined) {
    const secondsLeft = Math.max(0, Math.round((report.coversUntil * 1000 - nowMs) / 1000));
    return {
      show: true,
      tone: "partial",
      text:
        secondsLeft > 0
          ? `Automatic undo of the firewall/SDN part stops working in ${String(secondsLeft)}s (your Proxmox login expires before this window closes).`
          : "Automatic undo of the firewall/SDN part has already lapsed — your Proxmox login expired. Undo it by hand if you lose access.",
    };
  }
  return {
    show: true,
    tone: "ok",
    text: "The firewall/SDN part of this change will undo itself too if the window runs out.",
  };
}
