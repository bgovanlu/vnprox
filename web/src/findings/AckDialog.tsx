// SPDX-License-Identifier: Apache-2.0

// T-2402's acknowledge dialog: collects the REQUIRED reason and an optional
// expiry before muting a finding.
//
// The reason field is required here as well as on the server. That is not
// belt-and-braces for its own sake — the client-side requirement is what makes
// the rule legible to the operator (a disabled button with a hint beats a 400
// after they have typed nothing), and the server-side one is what makes it
// true. Only the second is load-bearing; see internal/findings/ack.go.
//
// Expiry is offered as a small set of durations rather than a date picker.
// "How long is this deliberate for" is the question an operator can actually
// answer, and every option here except "no expiry" produces a future instant,
// so the server's already-in-the-past refusal is unreachable from this UI by
// construction.
import { useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogClose, DialogContent } from "../components/Dialog";
import { DEFAULT_EXPIRY_DAYS, EXPIRY_CHOICES, expiryFromDays } from "./ackExpiry";

export interface AckDialogProps {
  /** The finding being acknowledged; the dialog is open iff this is set. */
  finding?: { id: string; detail: string };
  onCancel: () => void;
  /** `expiresAt` is unix seconds, or undefined for "no expiry". */
  onConfirm: (reason: string, expiresAt?: number) => void;
  pending?: boolean;
}

export function AckDialog({ finding, onCancel, onConfirm, pending }: AckDialogProps) {
  const [reason, setReason] = useState("");
  const [days, setDays] = useState(DEFAULT_EXPIRY_DAYS);

  const trimmed = reason.trim();
  const canConfirm = trimmed.length > 0 && !pending;

  return (
    <Dialog
      open={finding !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          setReason("");
          setDays(DEFAULT_EXPIRY_DAYS);
          onCancel();
        }
      }}
    >
      <DialogContent aria-label="Acknowledge finding">
        <h2 className="text-base font-semibold">Acknowledge this finding</h2>
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">{finding?.detail}</p>
        <p className="mt-3 text-xs text-fg-subtle">
          Acknowledging does not hide the finding or stop the check running. It records that this
          state is deliberate, so the stream can be triaged.
        </p>

        <label className="mt-4 flex flex-col gap-1 text-sm">
          <span className="font-medium">
            Reason <span className="text-red-600 dark:text-red-400">*</span>
          </span>
          <textarea
            value={reason}
            rows={3}
            onChange={(e) => { setReason(e.target.value); }}
            placeholder="Why is this state intentional?"
            className="rounded border border-slate-300 bg-transparent px-2 py-1 text-sm outline-none focus:border-accent-500 dark:border-slate-700"
          />
        </label>

        <label className="mt-3 flex items-center gap-2 text-sm">
          <span className="font-medium">Expires</span>
          <select
            aria-label="Acknowledgement expiry"
            value={days}
            onChange={(e) => { setDays(Number(e.target.value)); }}
            className="rounded border border-slate-300 bg-transparent px-1.5 py-0.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700"
          >
            {EXPIRY_CHOICES.map((c) => (
              <option key={c.days} value={c.days}>
                {c.label}
              </option>
            ))}
          </select>
        </label>

        <div className="mt-5 flex justify-end gap-2">
          <DialogClose asChild>
            <Button variant="secondary" size="sm">
              Cancel
            </Button>
          </DialogClose>
          <Button
            size="sm"
            disabled={!canConfirm}
            onClick={() => {
              onConfirm(trimmed, expiryFromDays(days, new Date()));
              setReason("");
              setDays(DEFAULT_EXPIRY_DAYS);
            }}
          >
            {pending ? "Acknowledging…" : "Acknowledge"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
