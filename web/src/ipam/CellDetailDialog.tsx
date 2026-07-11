// Allocation-grid cell detail popover (docs/features/ipam.md §2: "Click any
// cell -> detail (who, what, since when, source)" — "since when" is not
// carried, see internal/ipam/types.go's Cell doc comment) plus the
// reserve/release workflow (docs/features/ipam.md §3), landed as
// ipam.alloc.create/delete ops into the change drawer.
import { useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { inputClass } from "../changesets/editors/EditorDialog";
import { buildIpamAllocCreateOp, buildIpamAllocDeleteOp } from "../changesets/opBuilders";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { useToast } from "../components/Toast";
import type { IpamCell } from "../api/types";
import { MacPicker } from "../guests/MacPicker";
import { subnetRef } from "./ref";
import { confidenceLabel, stateLabel } from "./labels";

export interface CellDetailDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cell: IpamCell;
  subnetCidr: string;
  /** Read-only (non-SDN, bridge-derived) subnets have no reserve/release
   * affordance — docs/features/ipam.md §2: detected non-SDN subnets are
   * "read-only". */
  readOnly?: boolean;
}

export function CellDetailDialog({ open, onOpenChange, cell, subnetCidr, readOnly }: CellDetailDialogProps) {
  const { addOps } = useDrawerActions();
  const { toast } = useToast();
  const [hostname, setHostname] = useState("");
  const [mac, setMac] = useState("");

  const hasAllocation = (cell.sources ?? []).includes("pve-ipam");
  const canRelease = !readOnly && hasAllocation && cell.state !== "gateway";
  const canReserve = !readOnly && !hasAllocation && cell.state !== "gateway";

  async function handleReserve(): Promise<void> {
    const target = subnetRef(subnetCidr);
    const op = buildIpamAllocCreateOp(target, `${cell.ip}/32`, hostname || undefined, mac || undefined);
    try {
      await addOps([op], `Reserve ${cell.ip}`);
      toast({ title: `Reserve ${cell.ip} added to changeset` });
      onOpenChange(false);
    } catch {
      toast({ title: "Could not add reservation to changeset", variant: "error" });
    }
  }

  async function handleRelease(): Promise<void> {
    const target = subnetRef(subnetCidr);
    const op = buildIpamAllocDeleteOp(target, `${cell.ip}/32`);
    try {
      await addOps([op], `Release ${cell.ip}`);
      toast({ title: `Release ${cell.ip} added to changeset` });
      onOpenChange(false);
    } catch {
      toast({ title: "Could not add release to changeset", variant: "error" });
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm" aria-describedby="cell-detail-description">
        <DialogTitle>{cell.ip}</DialogTitle>
        <DialogDescription id="cell-detail-description">
          {stateLabel(cell.state)}
          {cell.confidence ? ` · ${confidenceLabel(cell.confidence)}` : ""}
        </DialogDescription>

        <dl className="mt-3 space-y-1 text-sm">
          {cell.hostname && (
            <div className="flex justify-between gap-4">
              <dt className="text-slate-500 dark:text-slate-400">Hostname</dt>
              <dd>{cell.hostname}</dd>
            </div>
          )}
          {cell.mac && (
            <div className="flex justify-between gap-4">
              <dt className="text-slate-500 dark:text-slate-400">MAC</dt>
              <dd className="font-mono">{cell.mac}</dd>
            </div>
          )}
          {cell.vmid !== undefined && cell.vmid > 0 && (
            <div className="flex justify-between gap-4">
              <dt className="text-slate-500 dark:text-slate-400">VMID</dt>
              <dd>{cell.vmid}</dd>
            </div>
          )}
          {cell.sources && cell.sources.length > 0 && (
            <div className="flex justify-between gap-4">
              <dt className="text-slate-500 dark:text-slate-400">Source</dt>
              <dd>{cell.sources.join(", ")}</dd>
            </div>
          )}
        </dl>

        {canReserve && (
          <div className="mt-4 space-y-2 border-t border-slate-200 pt-3 dark:border-slate-700">
            <p className="text-xs font-medium text-slate-600 dark:text-slate-300">Reserve this address</p>
            <input
              className={inputClass}
              placeholder="Hostname (optional)"
              value={hostname}
              onChange={(e) => {
                setHostname(e.target.value);
              }}
            />
            <input
              className={inputClass}
              placeholder="MAC (optional)"
              value={mac}
              onChange={(e) => {
                setMac(e.target.value);
              }}
            />
            {/* T-406: a reservation bound to a guest's MAC is exactly
             * ipam.alloc.create with a MAC — the same op handleReserve
             * already builds — so picking from a known guest NIC here is
             * just a convenience over typing the MAC by hand above; it
             * fills the same field, not a distinct code path. */}
            <MacPicker
              onPick={(pickedMac, guestLabel) => {
                setMac(pickedMac);
                if (!hostname) setHostname(guestLabel);
              }}
            />
            <Button variant="primary" size="sm" onClick={() => void handleReserve()}>
              Reserve {cell.ip}
            </Button>
          </div>
        )}

        {canRelease && (
          <div className="mt-4 border-t border-slate-200 pt-3 dark:border-slate-700">
            <Button variant="destructive" size="sm" onClick={() => void handleRelease()}>
              Release {cell.ip}
            </Button>
          </div>
        )}

        <div className="mt-4 flex justify-end">
          <Button variant="ghost" size="sm" onClick={() => { onOpenChange(false); }}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
