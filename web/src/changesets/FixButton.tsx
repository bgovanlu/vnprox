// One-click apply of a Finding's machine-applicable `fix` patch
// (docs/features/change-management.md §2: "Findings carry optional
// machine-applicable fix patches"; T-207 acceptance criterion 2: "a fix
// patch offers one-click apply"). Rendered on both surfaces a finding
// appears on — the drawer's op line and the review screen's error list —
// so it lives in its own module rather than in either screen's file.
import { useToast } from "../components/Toast";
import type { Changeset, Op } from "../api/types";
import { applyFixToOps } from "./findingFields";
import { useDrawerActions } from "./useDrawerActions";

export interface FixButtonProps {
  changeset: Changeset;
  fix: Op[];
}

export function FixButton({ changeset, fix }: FixButtonProps) {
  const { replaceOps } = useDrawerActions();
  const { toast } = useToast();
  return (
    <button
      type="button"
      className="ml-2 rounded bg-red-200 px-1.5 py-0.5 text-[11px] font-medium text-red-800 hover:bg-red-300 dark:bg-red-900 dark:text-red-100"
      onClick={() => {
        // PUT /changesets/{id} revalidates automatically, so the finding
        // disappears (or the fixed op's new value re-renders) on the next
        // cache update — no separate validate round trip needed.
        void replaceOps(applyFixToOps(changeset.ops, fix))
          .then(() => {
            toast({ title: "Fix applied" });
          })
          .catch(() => {
            toast({ title: "Could not apply fix", variant: "error" });
          });
      }}
    >
      fix
    </button>
  );
}
