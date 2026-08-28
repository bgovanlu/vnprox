// SPDX-License-Identifier: Apache-2.0

// The bridge delete-with-reattach flow (docs/features/change-management.md
// §5: "Deleting a bridge with attached guests requires choosing a
// reattachment target (generates the guest ops in the same changeset)").
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { buildBridgeDeleteOp, buildGuestReattachOps } from "../opBuilders";
import { useDrawerActions } from "../useDrawerActions";
import { EditorDialog, Field, inputClass } from "./EditorDialog";

export interface AttachedGuestNic {
  ref: string;
  label: string;
}

export interface BridgeDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target: string;
  bridgeName: string;
  attachedGuestNics: AttachedGuestNic[];
  /** Other bridges/VNets on this node a guest could reattach to. */
  reattachTargets: string[];
}

export function BridgeDeleteDialog({
  open,
  onOpenChange,
  node,
  target,
  bridgeName,
  attachedGuestNics,
  reattachTargets,
}: BridgeDeleteDialogProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const [reattachTo, setReattachTo] = useState(reattachTargets[0] ?? "");

  const disabledReason = missingCapTooltip(session, node, "netWrite");
  const needsReattach = attachedGuestNics.length > 0;
  const canSubmit = !needsReattach || reattachTo !== "";

  function handleSubmit(): void {
    if (needsReattach && !reattachTo) {
      toast({ title: "Choose a reattachment target", variant: "error" });
      return;
    }
    const ops = [
      ...(needsReattach ? buildGuestReattachOps(attachedGuestNics.map((g) => g.ref), reattachTo) : []),
      buildBridgeDeleteOp(target),
    ];
    void addOps(ops, `Delete ${bridgeName}`)
      .then(() => {
        onOpenChange(false);
        toast({ title: "Added to changeset" });
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete bridge ${bridgeName}`}
      description={
        needsReattach
          ? `${String(attachedGuestNics.length)} guest NIC(s) are attached — choose where they move to before deleting.`
          : "No guests are attached to this bridge."
      }
      onSubmit={handleSubmit}
      submitLabel="Delete"
      disabledReason={!capsForNode(session, node).netWrite ? disabledReason : !canSubmit ? "Choose a reattachment target first." : undefined}
    >
      {needsReattach && (
        <>
          <Field
            label="Attached guest NICs"
            help="Every guest NIC currently on this bridge. Deleting the bridge without moving them would leave each of these guests attached to nothing — which is why the target below is required rather than optional."
          >
            {/* Scrollable region -> must be keyboard-reachable (axe
                `scrollable-region-focusable`, WCAG 2.1.1). These are the
                guests this delete will move, so a keyboard user unable to
                scroll past the first few is a real loss. */}
            <ul
              className="max-h-28 space-y-0.5 overflow-y-auto text-xs text-slate-500 dark:text-slate-400"
              tabIndex={0}
              aria-label="Attached guest NICs"
            >
              {attachedGuestNics.map((g) => (
                <li key={g.ref}>{g.label}</li>
              ))}
            </ul>
          </Field>
          <Field label="Reattach to" help="Every guest above moves to this bridge/VNet in the same changeset.">
            <select className={inputClass} value={reattachTo} onChange={(e) => { setReattachTo(e.target.value); }}>
              <option value="" disabled>
                Choose a target…
              </option>
              {reattachTargets.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </Field>
        </>
      )}
    </EditorDialog>
  );
}
