// T-1705's Blueprint & plugin hub browse/install page. It lists the registry
// catalog (GET /hub/index) and installs an entry (POST /hub/install), reusing
// T-1107's trust-status vocabulary (unsigned / untrustedSignature /
// invalidSignature) for the shared signature gate and surfacing a plugin's
// declared capability scope for review *before* an install is confirmed
// (T-1705 AC4). The "vetted" badge is purely informational — installing a
// vetted-but-not-yet-trusted entry still requires the explicit trust step
// (T-1705 AC5); this page never bypasses that decision.
import { useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useToast } from "../components/Toast";
import { useHubInstallMutation, useHubIndexQuery } from "./queries";
import type { HubEntry, HubEntryType, HubInstallResponse } from "../api/types";

interface PendingTrust {
  entry: HubEntry;
  status: "unsigned" | "untrustedSignature";
}

export function HubPage() {
  const [activeType, setActiveType] = useState<HubEntryType>("blueprint");
  const indexQuery = useHubIndexQuery(activeType);
  const install = useHubInstallMutation();
  const { toast } = useToast();
  const [pending, setPending] = useState<PendingTrust | null>(null);

  function handleResult(entry: HubEntry, resp: HubInstallResponse): void {
    switch (resp.status) {
      case "imported":
        toast({ title: "Blueprint installed", description: entry.name, variant: "success" });
        break;
      case "installed":
        toast({ title: "Plugin installed", description: entry.name, variant: "success" });
        break;
      case "unsigned":
        setPending({ entry, status: "unsigned" });
        break;
      case "untrustedSignature":
        setPending({ entry, status: "untrustedSignature" });
        break;
      case "invalidSignature":
        toast({
          title: "Signature invalid",
          description: "This artifact's signature does not verify — it will not be installed.",
          variant: "error",
        });
        break;
    }
  }

  function doInstall(entry: HubEntry, trust?: { trustUnsigned?: boolean; trustNewKey?: boolean }): void {
    install.mutate(
      { type: entry.type, id: entry.id, version: entry.version, ...trust },
      {
        onSuccess: (resp) => { handleResult(entry, resp); },
        onError: () => { toast({ title: "Install failed", description: entry.name, variant: "error" }); },
      },
    );
  }

  function confirmTrust(): void {
    if (!pending) return;
    const { entry, status } = pending;
    setPending(null);
    doInstall(entry, status === "unsigned" ? { trustUnsigned: true } : { trustNewKey: true });
  }

  const entries = indexQuery.data?.items ?? [];

  return (
    <div className="hub-page" data-testid="hub-page">
      <header className="hub-page__header">
        <h1>Hub</h1>
        <p>Browse and install signed blueprint bundles and capability-scoped plugins.</p>
        <div role="tablist" aria-label="Hub artifact type" className="hub-page__tabs">
          <Button
            role="tab"
            aria-selected={activeType === "blueprint"}
            variant={activeType === "blueprint" ? "primary" : "secondary"}
            onClick={() => { setActiveType("blueprint"); }}
            data-testid="hub-tab-blueprint"
          >
            Blueprints
          </Button>
          <Button
            role="tab"
            aria-selected={activeType === "plugin"}
            variant={activeType === "plugin" ? "primary" : "secondary"}
            onClick={() => { setActiveType("plugin"); }}
            data-testid="hub-tab-plugin"
          >
            Plugins
          </Button>
        </div>
      </header>

      {indexQuery.isLoading && <p>Loading catalog…</p>}
      {indexQuery.isError && <p role="alert">The registry is unavailable. Check the configured [hub] registry_url.</p>}
      {!indexQuery.isLoading && !indexQuery.isError && entries.length === 0 && (
        <p data-testid="hub-empty">No {activeType} entries in the registry.</p>
      )}

      <ul className="hub-page__list">
        {entries.map((entry) => (
          <li key={`${entry.type}:${entry.id}`} className="hub-card" data-testid={`hub-entry-${entry.id}`}>
            <div className="hub-card__title">
              <span className="hub-card__name">{entry.name}</span>
              <span className="hub-card__version">v{entry.version}</span>
              {entry.vetted && (
                <span className="hub-badge hub-badge--vetted" data-testid={`hub-vetted-${entry.id}`}>
                  vetted
                </span>
              )}
              {entry.signed ? (
                <span className="hub-badge hub-badge--signed">signed</span>
              ) : (
                <span className="hub-badge hub-badge--unsigned">unsigned</span>
              )}
            </div>
            {entry.publisher && <div className="hub-card__publisher">{entry.publisher}</div>}
            {entry.description && <p className="hub-card__desc">{entry.description}</p>}

            {entry.type === "plugin" && entry.capabilities && entry.capabilities.length > 0 && (
              <div className="hub-card__caps" data-testid={`hub-caps-${entry.id}`}>
                <span className="hub-card__caps-label">Capabilities:</span>
                {entry.capabilities.map((cap) => (
                  <span key={cap} className="hub-badge hub-badge--cap">
                    {cap}
                  </span>
                ))}
              </div>
            )}

            <Button
              variant="primary"
              size="sm"
              disabled={install.isPending}
              onClick={() => { doInstall(entry); }}
              data-testid={`hub-install-${entry.id}`}
            >
              Install
            </Button>
          </li>
        ))}
      </ul>

      <Dialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null);
        }}
      >
        <DialogContent>
          {pending && (
            <>
              <DialogTitle>
                {pending.status === "unsigned" ? "Install an unsigned artifact?" : "Trust this signer?"}
              </DialogTitle>
              <DialogDescription>
                {pending.status === "unsigned"
                  ? `"${pending.entry.name}" has no signature. Installing it means trusting it with no cryptographic provenance.`
                  : `"${pending.entry.name}" is signed by a key this installation has not trusted yet${
                      pending.entry.vetted ? " (it is on the hub's vetted list, but that is informational only)" : ""
                    }. Trusting the signer pins its key for future installs.`}
              </DialogDescription>
              <div className="hub-dialog__actions">
                <Button
                  variant="secondary"
                  onClick={() => {
                    setPending(null);
                  }}
                >
                  Cancel
                </Button>
                <Button variant="primary" onClick={confirmTrust} data-testid="hub-confirm-trust">
                  {pending.status === "unsigned" ? "Trust & install" : "Trust signer & install"}
                </Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
