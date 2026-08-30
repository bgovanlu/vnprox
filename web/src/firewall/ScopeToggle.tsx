// SPDX-License-Identifier: Apache-2.0

// Scope enable/disable control with the "what will happen" summary
// (docs/features/firewall.md §2 — "the classic PVE footgun made visible").
// A plain checkbox plus scopeToggleSummary's golden-string explanation,
// shown *before* staging so the user sees the consequence up front, not
// buried in the changeset diff.
import { useState } from "react";
import { useToast } from "../components/Toast";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { buildFwOptionsUpdateOp } from "./opBuilders";
import { scopeToggleSummary, type FwToggleScope } from "./scopeSummary";

export interface ScopeToggleProps {
  scope: FwToggleScope;
  target: string;
  enabled: boolean;
  node?: string;
  guestLabel?: string;
  /** Required (and only meaningful) for scope "vnet" (T-3103) — the vnet's
   * display label (its bare name). */
  vnetLabel?: string;
}

export function ScopeToggle({ scope, target, enabled, node, guestLabel, vnetLabel }: ScopeToggleProps) {
  const { addOps } = useDrawerActions();
  const { toast } = useToast();
  const [pendingEnable, setPendingEnable] = useState<boolean | undefined>(undefined);

  const label =
    scope === "cluster" ? "Datacenter firewall"
    : scope === "node" ? `Node ${node ?? ""} firewall`
    : scope === "vnet" ? `VNet ${vnetLabel ?? ""} firewall`
    : `${guestLabel ?? "Guest"} firewall`;

  function requestToggle(next: boolean): void {
    setPendingEnable(next);
  }

  function confirmToggle(): void {
    if (pendingEnable === undefined) return;
    const op = buildFwOptionsUpdateOp(target, { enabled: pendingEnable });
    void addOps([op], `${pendingEnable ? "Enable" : "Disable"} ${label}`)
      .then(() => {
        toast({ title: "Staged in draft — review before applying", variant: "success" });
        setPendingEnable(undefined);
      })
      .catch((err: unknown) => {
        toast({ title: err instanceof Error ? err.message : "Failed to stage change", variant: "error" });
      });
  }

  return (
    <div className="flex flex-col gap-2 rounded border border-border p-3">
      <label className="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" checked={enabled} onChange={(e) => { requestToggle(e.target.checked); }} />
        {label} is {enabled ? "ON" : "OFF"}
      </label>
      {pendingEnable !== undefined && pendingEnable !== enabled && (
        <div className="flex flex-col gap-2 rounded bg-amber-50 p-2 text-xs text-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
          <p>{scopeToggleSummary({ scope, enabling: pendingEnable, node, guestLabel, vnetLabel })}</p>
          <div className="flex gap-2">
            <button type="button" onClick={confirmToggle} className="rounded bg-accent-600 px-2 py-1 font-medium text-white">
              Stage this change
            </button>
            <button type="button" onClick={() => { setPendingEnable(undefined); }} className="rounded px-2 py-1 text-fg-muted">
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
