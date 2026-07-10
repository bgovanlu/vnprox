// Shared editor submit flow: draft the op(s), then check the server's
// freshly-computed findings (T-202 validates on every draft change) for
// error-severity findings against the submitted target. Clean -> close the
// dialog and toast; errors -> keep the dialog open and hand back the
// per-field/general split so the originating form field renders its own
// error (T-207 acceptance criterion 2). The op stays in the drawer either
// way — the drawer line shows the same finding as a badge, and the user can
// fix it from either place (removing the op on error would fight the
// one-click `fix` flow and the drawer's own remove affordance).
import { useState } from "react";
import { useToast } from "../../components/Toast";
import type { Op } from "../../api/types";
import { editorFindingsFor, hasEditorErrors, type EditorFindings } from "../findingFields";
import { useDrawerActions } from "../useDrawerActions";

const NO_FINDINGS: EditorFindings = { byField: {}, general: [] };

export interface EditorSubmit {
  /** Per-field / general error split from the last submit (empty before
   * any submit, and after a clean one). */
  findings: EditorFindings;
  /** Drafts `ops` under `title`; closes via `onDone` when validation came
   * back clean for `targetRef`, otherwise keeps the dialog open with
   * `findings` populated. */
  submit: (ops: Op[], title: string, targetRef: string, onDone: () => void) => void;
}

export function useEditorSubmit(): EditorSubmit {
  const { amendLastOps } = useDrawerActions();
  const { toast } = useToast();
  const [findings, setFindings] = useState<EditorFindings>(NO_FINDINGS);
  // How many ops the previous (erroring) submit left at the draft's tail —
  // a re-submit amends them in place instead of appending duplicates.
  const [pendingCount, setPendingCount] = useState(0);

  function submit(ops: Op[], title: string, targetRef: string, onDone: () => void): void {
    void amendLastOps(pendingCount, ops, title)
      .then((updated) => {
        const split = editorFindingsFor(updated.findings, targetRef);
        setFindings(split);
        if (hasEditorErrors(split)) {
          setPendingCount(ops.length);
          toast({
            title: "Added to changeset with validation errors",
            description: "Fix them here or on the drawer line before applying.",
            variant: "error",
          });
          return;
        }
        setPendingCount(0);
        toast({ title: "Added to changeset" });
        onDone();
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  return { findings, submit };
}
