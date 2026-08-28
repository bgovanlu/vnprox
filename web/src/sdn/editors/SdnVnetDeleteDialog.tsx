// SPDX-License-Identifier: Apache-2.0

// The vnet delete-with-reattach flow — T-402 acceptance criterion 2,
// deliberately mirroring changesets/editors/BridgeDeleteDialog.tsx's T-203
// pattern exactly (same guard, same UI shape): a vnet with running guest
// NICs still attached can't be deleted outright (internal/change/
// validate_safety.go's vnetDeletionGuardFindings) unless the same changeset
// also reattaches every one — this dialog builds both the reattach ops and
// the delete op together so the backend's net-effect analysis sees them as
// one unit.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import { buildGuestReattachOps, buildSdnVnetDeleteOp } from "../../changesets/opBuilders";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { EditorDialog, Field, inputClass } from "../../changesets/editors/EditorDialog";
import type { AttachedGuestNic } from "../../changesets/entityCandidates";

export interface SdnVnetDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  target: string;
  vnetId: string;
  attachedGuestNics: AttachedGuestNic[];
  /** Other bridges/VNets a guest could reattach to. */
  reattachTargets: string[];
}

export function SdnVnetDeleteDialog({
  open,
  onOpenChange,
  target,
  vnetId,
  attachedGuestNics,
  reattachTargets,
}: SdnVnetDeleteDialogProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const [reattachTo, setReattachTo] = useState(reattachTargets[0] ?? "");

  const disabledReason = missingCapTooltip(session, "", "sdnWrite");
  const needsReattach = attachedGuestNics.length > 0;
  const canSubmit = !needsReattach || reattachTo !== "";

  function handleSubmit(): void {
    if (needsReattach && !reattachTo) {
      toast({ title: "Choose a reattachment target", variant: "error" });
      return;
    }
    const ops = [
      ...(needsReattach ? buildGuestReattachOps(attachedGuestNics.map((g) => g.ref), reattachTo) : []),
      buildSdnVnetDeleteOp(target),
    ];
    void addOps(ops, `Delete sdn vnet ${vnetId}`)
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
      title={`Delete VNet ${vnetId}`}
      description={
        needsReattach
          ? `${String(attachedGuestNics.length)} guest NIC(s) are attached — choose where they move to before deleting.`
          : "No guests are attached to this VNet."
      }
      onSubmit={handleSubmit}
      submitLabel="Delete"
      disabledReason={
        !hasAnyCap(session, "sdnWrite")
          ? disabledReason
          : !canSubmit
            ? "Choose a reattachment target first."
            : undefined
      }
    >
      {needsReattach && (
        <>
          <Field label="Attached guest NICs">
            {/* Scrollable region -> must be keyboard-reachable (axe
                `scrollable-region-focusable`, WCAG 2.1.1). These are the
                guests this delete will move, so a keyboard user unable to
                scroll past the first few is a real loss. */}
            <ul
              className="max-h-28 space-y-0.5 overflow-y-auto text-xs text-fg-subtle"
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
