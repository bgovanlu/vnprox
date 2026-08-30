// SPDX-License-Identifier: Apache-2.0

// T-2003's review-approval panel: shows the current decision and lets a
// reviewer approve or reject. This is a UI convenience only — whether apply
// actually requires an approved decision is decided server-side
// (internal/change's beginApply reads changeset_approvals fresh on every
// apply attempt); this panel never grants anything by itself, and a client
// that never renders it (or a modified build that hides the reject button)
// changes nothing about what the server will accept. See
// ReviewApplyScreen.tsx's own Apply-button disabling for the client-side
// echo of that same server truth.
import { useState } from "react";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import type { ApprovalState } from "../api/types";
import { useReviewApproveMutation, useReviewRejectMutation } from "./queries";

export interface ApprovalPanelProps {
  changesetId: string;
  approval: ApprovalState | undefined;
}

const statusLabel: Record<ApprovalState["status"], string> = {
  none: "Not yet reviewed",
  approved: "Approved",
  rejected: "Rejected",
};

const statusClass: Record<ApprovalState["status"], string> = {
  none: "border-border-strong bg-slate-50 text-fg-body dark:bg-slate-800",
  approved:
    "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
  rejected: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200",
};

function formatTimestamp(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

export function ApprovalPanel({ changesetId, approval }: ApprovalPanelProps) {
  const [rejectReason, setRejectReason] = useState("");
  const [showRejectForm, setShowRejectForm] = useState(false);
  const approveMutation = useReviewApproveMutation();
  const rejectMutation = useReviewRejectMutation();
  const { toast } = useToast();

  const status = approval?.status ?? "none";
  const required = approval?.required ?? false;

  async function handleApprove(): Promise<void> {
    try {
      await approveMutation.mutateAsync(changesetId);
    } catch {
      toast({ title: "Could not record approval", description: "See the server's response for why.", variant: "error" });
    }
  }

  async function handleReject(): Promise<void> {
    try {
      await rejectMutation.mutateAsync({ id: changesetId, reason: rejectReason.trim() || undefined });
      setShowRejectForm(false);
      setRejectReason("");
    } catch {
      toast({ title: "Could not record rejection", variant: "error" });
    }
  }

  return (
    <div
      className={`mt-3 rounded-md border p-3 text-xs ${statusClass[status]}`}
      role="group"
      aria-label="Review approval"
      data-testid="approval-panel"
    >
      <div className="flex items-center justify-between gap-2">
        <div>
          <p className="flex items-center gap-1.5 font-medium">
            <span>
              {statusLabel[status]}
              {required && status !== "approved" && " — required before this changeset can apply"}
            </span>
            <HelpAnchor topic="changeset-approvals" />
          </p>
          {approval?.decidedBy && approval.decidedAt !== undefined && (
            <p className="mt-0.5 text-[11px] opacity-80">
              by {approval.decidedBy} · {formatTimestamp(approval.decidedAt)}
              {status === "rejected" && approval.reason && <>: “{approval.reason}”</>}
            </p>
          )}
          {!required && (
            <p className="mt-0.5 text-[11px] opacity-70">
              This deployment does not require approval before apply — recording one here is optional, for the
              record.
            </p>
          )}
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button
            variant="primary"
            size="sm"
            disabled={approveMutation.isPending || status === "approved"}
            onClick={() => void handleApprove()}
          >
            Approve
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={rejectMutation.isPending}
            onClick={() => {
              setShowRejectForm((v) => !v);
            }}
          >
            Reject
          </Button>
        </div>
      </div>
      {showRejectForm && (
        <div className="mt-2 flex items-center gap-2">
          <input
            type="text"
            value={rejectReason}
            onChange={(e) => {
              setRejectReason(e.target.value);
            }}
            placeholder="Reason (optional)"
            aria-label="Rejection reason"
            className="flex-1 rounded border border-border-strong px-1.5 py-0.5 text-xs dark:bg-slate-900"
          />
          <Button variant="destructive" size="sm" onClick={() => void handleReject()}>
            Confirm reject
          </Button>
        </div>
      )}
    </div>
  );
}
