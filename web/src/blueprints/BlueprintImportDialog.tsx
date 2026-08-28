// SPDX-License-Identifier: Apache-2.0

// T-1107's import dialog: surfaces POST /blueprints/import's three
// non-error signature states (docs/features/blueprints.md §5) and gates
// the actual import behind an explicit trust step for two of them —
// never a silent import of anything this installation doesn't already
// trust.
//
//   - "imported"           — already-trusted signer (or a previous trust
//     decision) verified immediately: no prompt, this dialog just closes.
//   - "unsigned"           — no signature at all. Importing requires
//     checking "I trust this file" and re-submitting with
//     `trustUnsigned: true`.
//   - "untrustedSignature" — the signature verifies, but against a key
//     this installation hasn't pinned yet. Importing requires checking
//     "I trust this signer" and re-submitting with `trustNewKey: true`
//     (which also pins the key for future imports).
//   - "invalidSignature"   — the signature does not verify at all (bad
//     encoding, or the content was tampered with after signing). There is
//     no trust step that can fix this; the dialog only offers Cancel.
import { useEffect, useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { HelpAnchor } from "../help/HelpAnchor";
import { useToast } from "../components/Toast";
import type { Blueprint, BlueprintBundle, ImportBundleRequest, ImportBundleResponse } from "../api/types";
import { useImportBundleMutation } from "./queries";

export interface BlueprintImportDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The parsed bundle to import, or a legacy plain-Blueprint file wrapped
   * as `{bundleVersion: 1, blueprint}` by the caller (BlueprintsPage's
   * file picker) — null while no file has been chosen yet. */
  bundle: BlueprintBundle | null;
  onImported: (bp: Blueprint) => void;
}

export function BlueprintImportDialog({ open, onOpenChange, bundle, onImported }: BlueprintImportDialogProps) {
  const importMutation = useImportBundleMutation();
  const { toast } = useToast();
  const [trustConfirmed, setTrustConfirmed] = useState(false);
  const [probeResult, setProbeResult] = useState<ImportBundleResponse | null>(null);
  const [probeError, setProbeError] = useState(false);

  function handleImported(resp: ImportBundleResponse): void {
    setProbeResult(resp);
    if (resp.status === "imported" && resp.blueprint) {
      onImported(resp.blueprint);
      onOpenChange(false);
      toast({ title: "Blueprint imported", description: resp.blueprint.name, variant: "success" });
    }
  }

  // A freshly opened bundle always gets one plain probe call (no trust
  // flags) — this alone is enough to import an already-trusted signer's
  // bundle (AC2: "no prompt"), and for every other case it's what tells
  // this dialog which of the three prompts to show.
  useEffect(() => {
    if (!open || !bundle) {
      setProbeResult(null);
      setTrustConfirmed(false);
      setProbeError(false);
      return;
    }
    setTrustConfirmed(false);
    setProbeResult(null);
    setProbeError(false);
    importMutation.mutate(bundle, {
      onSuccess: handleImported,
      onError: () => {
        setProbeError(true);
      },
    });
    // Only re-probe when the dialog opens against a (possibly new) bundle —
    // not on every render of the mutation object itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, bundle]);

  function handleConfirmImport(): void {
    if (!bundle || !probeResult) return;
    const req: ImportBundleRequest = {
      ...bundle,
      trustUnsigned: probeResult.status === "unsigned",
      trustNewKey: probeResult.status === "untrustedSignature",
    };
    importMutation.mutate(req, {
      onSuccess: handleImported,
      onError: () => {
        toast({ title: "Could not import blueprint", variant: "error" });
      },
    });
  }

  const status = probeResult?.status;
  const needsTrustStep = status === "unsigned" || status === "untrustedSignature";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <div className="flex items-center gap-1.5">
          <DialogTitle>Import blueprint</DialogTitle>
          <HelpAnchor topic="blueprint-import" />
        </div>
        <DialogDescription>
          {!bundle ? "Choose a blueprint bundle file to import." : !probeResult && !probeError ? "Checking signature…" : null}
        </DialogDescription>

        {probeError ? (
          <p className="mt-3 text-sm text-red-600 dark:text-red-400" data-testid="import-error">
            Could not check this bundle's signature. Try again.
          </p>
        ) : null}

        {status === "unsigned" ? (
          <div className="mt-3 space-y-3" data-testid="import-state-unsigned">
            <p className="text-sm text-amber-700 dark:text-amber-400">
              This blueprint file is unsigned — there is no way to verify who created it.
            </p>
            <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
              <input
                type="checkbox"
                checked={trustConfirmed}
                onChange={(e) => {
                  setTrustConfirmed(e.target.checked);
                }}
              />
              I trust this file and want to import it anyway
            </label>
          </div>
        ) : null}

        {status === "untrustedSignature" ? (
          <div className="mt-3 space-y-3" data-testid="import-state-untrusted">
            <p className="text-sm text-amber-700 dark:text-amber-400">
              This blueprint is signed by a key vnprox does not yet trust.
            </p>
            <p className="break-all text-xs text-slate-500 dark:text-slate-400">
              Signer fingerprint: <code>{probeResult?.signer?.fingerprint}</code>
            </p>
            <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
              <input
                type="checkbox"
                checked={trustConfirmed}
                onChange={(e) => {
                  setTrustConfirmed(e.target.checked);
                }}
              />
              I trust this signer and want to pin their key
            </label>
          </div>
        ) : null}

        {status === "invalidSignature" ? (
          <p className="mt-3 text-sm text-red-600 dark:text-red-400" data-testid="import-state-invalid">
            This bundle&apos;s signature does not verify — the content may have been altered after it was signed. It
            cannot be imported.
          </p>
        ) : null}

        <div className="mt-5 flex justify-end gap-2">
          <Button
            variant="secondary"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          {needsTrustStep ? (
            <Button
              variant="primary"
              disabled={!trustConfirmed || importMutation.isPending}
              onClick={handleConfirmImport}
            >
              Import anyway
            </Button>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
