// SPDX-License-Identifier: Apache-2.0

// T-2805 — the advisory-lock notice inside the changeset drawer.
//
// Two operators staging conflicting changes to the same bridge used to be
// invisible to both of them. This is where the second one finds out. It is
// the ENTIRE consequence of a lock: the changes it warns about are already
// staged, no control on this screen is disabled because of it, and the apply
// path never sees it. A lock prevents an accidental change, never an
// emergency one.
//
// The "take over" button is therefore not "unlock and continue" — the work is
// already saved. It transfers the claim so the *other* operator is the one
// warned next time, and records the takeover in the audit log.
import { Button } from "../components/Button";
import type { Changeset } from "../api/types";
import { lockNotice } from "./lockWarning";
import { useLockNoticeStore } from "./lockNoticeStore";
import { useDrawerActions } from "./useDrawerActions";

export interface LockNoticeBannerProps {
  changeset: Changeset;
}

export function LockNoticeBanner({ changeset }: LockNoticeBannerProps) {
  const noticeFor = useLockNoticeStore((s) => s.changesetId);
  const locks = useLockNoticeStore((s) => s.locks);
  const { replaceOps } = useDrawerActions();

  // A warning belongs to the draft it was raised for; never render one draft's
  // collision over another's.
  const notice = lockNotice(noticeFor === changeset.id ? locks : undefined);
  if (!notice.show) return null;

  const isOverride = notice.kind === "override";

  return (
    <div
      role="status"
      data-testid="lock-notice"
      className={
        isOverride
          ? "rounded-md border border-slate-300 bg-slate-50 p-3 text-sm text-slate-800 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
          : "rounded-md border border-amber-400 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-600 dark:bg-amber-950 dark:text-amber-100"
      }
    >
      <p className="font-medium">{notice.heading}</p>
      <ul className="mt-1 list-disc pl-5">
        {notice.lines.map((line) => (
          <li key={line}>{line}</li>
        ))}
      </ul>
      <p className="mt-2">{notice.action}</p>
      {!isOverride && (
        <div className="mt-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              // Re-submits the SAME ops. Nothing about the changeset changes;
              // only the claim moves, and the move is audited.
              void replaceOps(changeset.ops, { lockOverride: true });
            }}
          >
            Take over the lock
          </Button>
        </div>
      )}
    </div>
  );
}
