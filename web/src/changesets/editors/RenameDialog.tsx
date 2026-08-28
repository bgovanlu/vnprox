// SPDX-License-Identifier: Apache-2.0

// Rename a logical interface (bridge/bond/vlan) — issue #2. Drafts a single
// iface.rename op; the change engine rewrites the stanza, its auto/allow
// references, and every in-file reference to the old name, and blocks the
// rename if guests are still attached to the old name (their bridge= binding
// isn't rewritten by a rename).
//
// The dialog is also where issue #2's "temporary until reboot / red
// asterisk" guidance lives: the proposed name is shown with a red asterisk
// and an inline explanation that a rename is only fully live once applied —
// and, for the parts of a rename the kernel realizes at boot (device names),
// permanent after a reboot.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { buildIfaceRenameOp } from "../opBuilders";
import { useDrawerActions } from "../useDrawerActions";
import { EditorDialog, Field, FieldHelp, inputClass } from "./EditorDialog";
import { ifaceNameError } from "./ifaceName";

export interface RenameDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target: string;
  /** The interface's current name (for the no-op check and the heading). */
  currentName: string;
}

export function RenameDialog({ open, onOpenChange, node, target, currentName }: RenameDialogProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const [newName, setNewName] = useState("");

  const nameErr = ifaceNameError(newName, currentName);
  const canWrite = capsForNode(session, node).netWrite;
  const disabledReason = !canWrite ? missingCapTooltip(session, node, "netWrite") : nameErr;

  function handleSubmit(): void {
    if (nameErr) {
      toast({ title: nameErr, variant: "error" });
      return;
    }
    void addOps([buildIfaceRenameOp(target, newName.trim())], `Rename ${currentName} → ${newName.trim()}`)
      .then(() => {
        onOpenChange(false);
        toast({ title: "Added to changeset", description: "Review before applying." });
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Rename ${currentName}`}
      description="Renames this interface and every reference to it in this node's network config."
      onSubmit={handleSubmit}
      submitLabel="Add rename"
      disabledReason={disabledReason}
    >
      <Field label="New name" help="Letters, digits, and .-_ — e.g. vmbrmgmt. At most 15 characters." errors={nameErr && newName ? [nameErr] : undefined}>
        <input className={inputClass} value={newName} onChange={(e) => { setNewName(e.target.value); }} placeholder={currentName} autoFocus />
      </Field>

      {newName.trim() && !nameErr && (
        <p className="text-xs text-slate-600 dark:text-slate-300">
          Will become{" "}
          <span className="font-medium">
            {newName.trim()}
            <span className="text-red-600 dark:text-red-400" title="Temporary until applied — a physical NIC's device name only changes permanently after a reboot.">
              *
            </span>
          </span>
        </p>
      )}

      <FieldHelp>
        <span className="text-red-600 dark:text-red-400">*</span> The new name is staged, not live yet: it takes effect when
        you apply this changeset. A logical interface (bridge/bond/VLAN) is renamed in place; a physical NIC&apos;s device
        name is fixed at boot, so that kind of rename is only permanent after a reboot. Guests still attached to the old
        name must be reattached in the same changeset — the engine will tell you if any are.
      </FieldHelp>
    </EditorDialog>
  );
}
