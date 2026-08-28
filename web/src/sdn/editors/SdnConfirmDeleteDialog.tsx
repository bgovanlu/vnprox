// SPDX-License-Identifier: Apache-2.0

// Plain confirm-delete for sdn.zone/sdn.subnet (no attached-guest interlock
// the way a vnet delete has — see SdnVnetDeleteDialog for that flow. A zone
// still carries T-402's other pre-apply validators (bridge existence, node
// coverage) and a subnet's deletion-with-allocations guard is a documented,
// deferred gap — see the T-402 report — so this dialog is deliberately a
// plain "are you sure", same shape BondEditor's own delete affordance uses
// for bond.delete, which likewise has no interlock).
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { EditorDialog } from "../../changesets/editors/EditorDialog";
import type { Op } from "../../api/types";

export interface SdnConfirmDeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  op: Op;
  changesetTitle: string;
}

export function SdnConfirmDeleteDialog({ open, onOpenChange, title, description, op, changesetTitle }: SdnConfirmDeleteDialogProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    void addOps([op], changesetTitle)
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
      title={title}
      description={description}
      onSubmit={handleSubmit}
      submitLabel="Delete"
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
    >
      <p className="text-sm text-slate-500 dark:text-slate-400">This adds a delete op to the current changeset draft — nothing is removed until the changeset applies.</p>
    </EditorDialog>
  );
}
