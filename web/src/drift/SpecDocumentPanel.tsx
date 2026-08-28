// SPDX-License-Identifier: Apache-2.0

// T-3001: the document itself — `GET /spec` (render the live cluster as one)
// and the pin (`GET`/`POST`/`DELETE /spec/pin`).
//
// Pinning stores a document and nothing else: it is app-owned data the
// `spec_drift`/`spec_reconciliation` checks compare against, never a shadow
// copy of PVE config and never an apply. Unpinning removes that comparison —
// it does not remove anything from the cluster.
//
// The working document in the editor below is deliberately just text the
// operator holds: it is what the plan panel diffs, and it becomes the pin only
// when they say so.
import { useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import { instantLabel } from "./gitsyncState";
import { useExportSpecMutation, usePinSpecMutation, useSpecPinQuery, useUnpinSpecMutation } from "./queries";

interface SpecDocumentPanelProps {
  content: string;
  onContentChange: (content: string) => void;
  /** Tooltip naming the missing capability, or undefined when allowed. */
  writeDisabledReason?: string;
}

export function SpecDocumentPanel({ content, onContentChange, writeDisabledReason }: SpecDocumentPanelProps) {
  const { data: pin, isLoading, error } = useSpecPinQuery();
  const exportMutation = useExportSpecMutation();
  const pinMutation = usePinSpecMutation();
  const unpinMutation = useUnpinSpecMutation();
  const { toast } = useToast();
  const [confirmUnpin, setConfirmUnpin] = useState(false);
  const [actionError, setActionError] = useState<string | undefined>(undefined);

  const canWrite = writeDisabledReason === undefined;

  async function loadLive(): Promise<void> {
    setActionError(undefined);
    try {
      const exported = await exportMutation.mutateAsync();
      onContentChange(exported.content);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "could not render the live cluster as a spec");
    }
  }

  async function pinCurrent(): Promise<void> {
    setActionError(undefined);
    try {
      await pinMutation.mutateAsync(content);
      toast({
        title: "Document pinned",
        description: "Drift checks now compare live state against it. Nothing was applied.",
        variant: "success",
      });
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "could not pin the document");
    }
  }

  async function unpin(): Promise<void> {
    setConfirmUnpin(false);
    setActionError(undefined);
    try {
      await unpinMutation.mutateAsync();
      toast({ title: "Pin cleared", description: "The cluster itself is unchanged.", variant: "success" });
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "could not clear the pin");
    }
  }

  return (
    <section aria-labelledby="spec-document-heading">
      <h2 id="spec-document-heading" className="flex items-center gap-2 text-lg font-semibold">
        The document
        <HelpAnchor topic="spec-pin" />
      </h2>

      {isLoading && <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">Reading the pinned document…</p>}

      {error !== null && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-slate-300 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-900"
        >
          <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">
            Could not read whether a document is pinned
          </p>
          <p className="mt-1 text-xs text-slate-600 dark:text-slate-400">
            That is not the same as nothing being pinned — vnprox could not ask.
          </p>
        </div>
      )}

      {pin !== undefined && !pin.pinned && (
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
          No document is pinned. Without one, and without a git sync, there is no spec position to compare
          against — only config and live.
        </p>
      )}

      {pin !== undefined && pin.pinned && (
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
          Pinned by <strong>{pin.pinnedBy ?? "an unrecorded user"}</strong> at{" "}
          {instantLabel(pin.pinnedAt, "an unrecorded time")}. Drift checks compare live state against exactly this
          document.
        </p>
      )}

      {actionError !== undefined && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-status-critical bg-status-critical-soft p-3 text-sm"
        >
          <p className="font-semibold text-slate-900 dark:text-slate-100">The daemon refused that</p>
          <p className="mt-1 text-slate-800 dark:text-slate-100">{actionError}</p>
        </div>
      )}

      <div className="mt-3 flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="secondary"
          disabled={exportMutation.isPending}
          onClick={() => {
            void loadLive();
          }}
        >
          {exportMutation.isPending ? "Rendering…" : "Render the live cluster"}
        </Button>
        {pin?.pinned === true && pin.content !== undefined && (
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              onContentChange(pin.content ?? "");
            }}
          >
            Load the pinned document
          </Button>
        )}
        <Button
          size="sm"
          disabled={!canWrite || content.trim() === "" || pinMutation.isPending}
          title={writeDisabledReason}
          onClick={() => {
            void pinCurrent();
          }}
        >
          {pinMutation.isPending ? "Pinning…" : "Pin this document"}
        </Button>
        {pin?.pinned === true && (
          <Button
            size="sm"
            variant="secondary"
            disabled={!canWrite || unpinMutation.isPending}
            title={writeDisabledReason}
            onClick={() => {
              setConfirmUnpin(true);
            }}
          >
            Unpin…
          </Button>
        )}
      </div>

      <label className="mt-3 flex flex-col gap-1 text-sm">
        <span className="font-medium">Working document</span>
        <textarea
          aria-label="Spec document"
          value={content}
          rows={10}
          spellCheck={false}
          onChange={(e) => {
            onContentChange(e.target.value);
          }}
          placeholder="Paste a specVersion: 1 document, or render the live cluster as one."
          className="rounded border border-slate-300 bg-transparent px-2 py-1 font-mono text-xs outline-none focus:border-accent-500 dark:border-slate-700"
        />
      </label>

      <Dialog
        open={confirmUnpin}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmUnpin(false);
          }
        }}
      >
        <DialogContent aria-label="Confirm unpin">
          <DialogTitle>Clear the pinned document?</DialogTitle>
          <DialogDescription>
            The drift checks lose their spec position: entities stop being compared against any declared intent.
          </DialogDescription>
          <p className="mt-3 text-sm text-slate-600 dark:text-slate-300">
            Nothing on the cluster changes, and no changeset is created. Pinning a document again restores the
            comparison.
          </p>
          <div className="mt-5 flex justify-end gap-2">
            <DialogClose asChild>
              <Button variant="secondary" size="sm">
                Cancel
              </Button>
            </DialogClose>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => {
                void unpin();
              }}
            >
              Clear the pin
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  );
}
