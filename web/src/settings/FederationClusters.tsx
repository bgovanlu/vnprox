// SPDX-License-Identifier: Apache-2.0

// T-2001's federation cluster editor: `/federation/clusters` has had full
// CRUD, audit coverage, and capability gating since T-1201 and no UI at
// all — attaching a cluster meant hand-crafting a POST with a credential in
// it. Mirrors AlertRules.tsx's list+detail-panel layout (a list of
// attached clusters on the left, the attach/edit form for the selected one
// on the right).
//
// The credential is write-only end to end: GET /federation/clusters never
// returns it (internal/api/federation.go's own doc comment), so this form
// never has plaintext to rehydrate into an edit — `toFormState` always
// starts credential fields blank and `credentialTouched: false`, and only
// an operator who explicitly types a new one sends `credential` on save.
// Leaving it untouched (a rename or a wgTunnelId-only edit) omits
// `credential` from the PUT body entirely, matching docs/api.md's "an
// absent/null credential leaves the stored one untouched" contract.
import { useState } from "react";
import clsx from "clsx";
import type { FederationCluster, FederationCredentialRequest } from "../api/federation";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useToast } from "../components/Toast";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Tooltip } from "../components/Tooltip";
import { Button } from "../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import {
  useCreateFederationClusterMutation,
  useDeleteFederationClusterMutation,
  useFederationClustersQuery,
  useUpdateFederationClusterMutation,
} from "./federationClustersQueries";

interface FormState {
  name: string;
  apiUrl: string;
  credentialKind: "ticket" | "token";
  username: string;
  password: string;
  realm: string;
  token: string;
  /** Whether the operator touched a credential field this session — an
   * untouched credential on an edit means "leave the stored one
   * unchanged", matching AlertRules.tsx's targetSecret precedent for the
   * identical write-only-field problem. Always true in effect for a
   * brand-new attach (validate() requires it before Save enables). */
  credentialTouched: boolean;
}

const EMPTY_FORM: FormState = {
  name: "",
  apiUrl: "",
  credentialKind: "ticket",
  username: "",
  password: "",
  realm: "",
  token: "",
  credentialTouched: false,
};

function toFormState(cluster: FederationCluster): FormState {
  return { ...EMPTY_FORM, name: cluster.name, apiUrl: cluster.apiUrl };
}

function toCredentialRequest(form: FormState): FederationCredentialRequest {
  return form.credentialKind === "ticket"
    ? { kind: "ticket", username: form.username.trim(), password: form.password, realm: form.realm.trim() || undefined }
    : { kind: "token", token: form.token.trim() };
}

/** Client-side mirror of internal/api/federation.go's own request
 * validation — avoids a round trip for the obvious cases and gives Save
 * something to disable on, exactly like AlertRules.tsx's validate(). The
 * server remains the source of truth. */
function validate(form: FormState, credentialRequired: boolean): string | undefined {
  if (!form.name.trim()) return "Name is required.";
  try {
    const u = new URL(form.apiUrl);
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      return "API URL must be http:// or https://.";
    }
  } catch {
    return "API URL must be an absolute http(s) URL.";
  }
  if (credentialRequired || form.credentialTouched) {
    if (form.credentialKind === "ticket") {
      if (!form.username.trim()) return "Username is required.";
      if (!form.password) return "Password is required.";
    } else if (!form.token.trim()) {
      return "API token is required.";
    }
  }
  return undefined;
}

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === "ok"
      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
      : status === "unreachable"
        ? "bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300"
        : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400";
  return <span className={clsx("rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide", cls)}>{status}</span>;
}

/** The read-only tunnel-linkage summary — wgTunnelSource's first UI
 * consumer (T-1407-followups). "explicit" is an operator's own PUT
 * override; "peer" is derived from a WireGuard peer tagged with this
 * cluster (the connect-clusters wizard's "Federated cluster" field). Both
 * render the same non-obvious consequence docs/api.md's Federation section
 * states: clearing an explicit override does not unlink a cluster that
 * still has a tagged peer — it just falls back to "peer". */
function TunnelLinkage({
  cluster,
  canWrite,
  disabledReason,
  onClear,
  clearing,
}: {
  cluster: FederationCluster;
  canWrite: boolean;
  disabledReason: string | undefined;
  onClear: () => void;
  clearing: boolean;
}) {
  if (!cluster.wgTunnelId) {
    return (
      <div className="text-sm text-slate-600 dark:text-slate-400" data-testid="tunnel-linkage">
        Not tunnel-linked. Tag a WireGuard peer with this cluster in the tunnel wizard to link one, or set an
        explicit override via <code>PUT /federation/clusters/{"{id}"}</code>.
      </div>
    );
  }
  const isExplicit = cluster.wgTunnelSource === "explicit";
  return (
    <div className="space-y-1.5 text-sm" data-testid="tunnel-linkage">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium text-slate-700 dark:text-slate-200">{cluster.wgTunnelId}</span>
        <span
          className={clsx(
            "rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide",
            isExplicit
              ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
              : "bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300",
          )}
        >
          {isExplicit ? "explicit override" : "derived from tagged peer"}
        </span>
      </div>
      {isExplicit ? (
        <>
          <p className="text-xs text-slate-600 dark:text-slate-400">
            Set explicitly through this registry. Clearing it does <strong>not</strong> unlink this cluster if a
            WireGuard peer is still tagged with it — the linkage then falls back to that peer-derived link instead
            of disappearing. To fully unlink, retag or remove that peer through the WireGuard tunnel editor.
          </p>
          <Tooltip content={disabledReason}>
            <span>
              <Button size="sm" variant="secondary" disabled={!canWrite || clearing} onClick={onClear}>
                Clear override
              </Button>
            </span>
          </Tooltip>
        </>
      ) : (
        <p className="text-xs text-slate-600 dark:text-slate-400">
          Derived from a WireGuard peer tagged with this cluster — this screen cannot change it. Retag or remove
          that peer through the WireGuard tunnel editor to unlink.
        </p>
      )}
    </div>
  );
}

export function FederationClusters() {
  const { data: clusters, isLoading, error } = useFederationClustersQuery();
  const { data: session } = useSession();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [detachTarget, setDetachTarget] = useState<FederationCluster | undefined>(undefined);
  const { toast } = useToast();

  const createMutation = useCreateFederationClusterMutation();
  const updateMutation = useUpdateFederationClusterMutation();
  const deleteMutation = useDeleteFederationClusterMutation();

  // The registry is a cluster-wide affordance (this daemon's own local
  // registration row, not any specific node's config) — gated the same way
  // AlertRules.tsx gates its own cluster-wide netWrite affordance, via the
  // "" cluster-wide capability entry rather than a specific node.
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  const items = clusters ?? [];
  const selected = items.find((c) => c.id === selectedId);
  const editing = creating || selected !== undefined;

  function startCreate(): void {
    setCreating(true);
    setSelectedId(undefined);
    setForm(EMPTY_FORM);
  }

  function selectCluster(cluster: FederationCluster): void {
    setCreating(false);
    setSelectedId(cluster.id);
    setForm(toFormState(cluster));
  }

  function cancelEdit(): void {
    setCreating(false);
    setForm(EMPTY_FORM);
  }

  const validationError = editing ? validate(form, creating) : undefined;

  async function handleSave(): Promise<void> {
    if (validationError) return;
    try {
      if (creating) {
        const created = await createMutation.mutateAsync({
          name: form.name.trim(),
          apiUrl: form.apiUrl.trim(),
          credential: toCredentialRequest(form),
        });
        setCreating(false);
        setSelectedId(created.id);
        setForm(toFormState(created));
        toast({ title: "Cluster attached", description: created.name, variant: "success" });
      } else if (selected) {
        const updated = await updateMutation.mutateAsync({
          id: selected.id,
          req: {
            name: form.name.trim(),
            apiUrl: form.apiUrl.trim(),
            ...(form.credentialTouched ? { credential: toCredentialRequest(form) } : {}),
          },
        });
        setForm(toFormState(updated));
        toast({ title: "Cluster saved", description: updated.name, variant: "success" });
      }
    } catch {
      toast({ title: creating ? "Could not attach cluster" : "Could not save cluster", variant: "error" });
    }
  }

  async function handleClearTunnelOverride(cluster: FederationCluster): Promise<void> {
    try {
      const updated = await updateMutation.mutateAsync({
        id: cluster.id,
        req: { name: cluster.name, apiUrl: cluster.apiUrl, wgTunnelId: "" },
      });
      if (selectedId === cluster.id) setForm(toFormState(updated));
      toast({ title: "Explicit tunnel override cleared", description: updated.name, variant: "success" });
    } catch {
      toast({ title: "Could not clear tunnel override", variant: "error" });
    }
  }

  async function handleConfirmDetach(): Promise<void> {
    const cluster = detachTarget;
    if (!cluster) return;
    try {
      await deleteMutation.mutateAsync(cluster.id);
      if (selectedId === cluster.id) {
        setSelectedId(undefined);
        setForm(EMPTY_FORM);
      }
      toast({ title: "Cluster detached", description: cluster.name, variant: "success" });
    } catch {
      toast({ title: "Could not detach cluster", variant: "error" });
    } finally {
      setDetachTarget(undefined);
    }
  }

  if (isLoading) {
    return <EmptyState title="Loading…" description="Fetching attached clusters." />;
  }
  if (error) {
    return <EmptyState title="Could not load attached clusters" description="Check your connection and try again." />;
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-hidden p-4">
      <PageHeader
        title="Federated clusters"
        description="Attach other Proxmox clusters for aggregated read views and cross-cluster IPAM conflict detection. vnprox
          never writes to an attached cluster's own config — federation federates views, not config ownership."
      />

      <div className="flex flex-1 gap-4 overflow-hidden">
        <div className="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">Clusters</h2>
            <Tooltip content={writeDisabledReason}>
              <span>
                <Button size="sm" variant="secondary" disabled={!canWrite} onClick={startCreate}>
                  Attach cluster
                </Button>
              </span>
            </Tooltip>
          </div>

          {items.length === 0 ? (
            <p className="text-sm text-slate-600 dark:text-slate-400">No clusters attached yet.</p>
          ) : (
            <ul className="flex flex-col gap-1" data-testid="federation-cluster-list">
              {items.map((cluster) => (
                <li key={cluster.id}>
                  <button
                    type="button"
                    className={clsx(
                      "flex w-full flex-col items-start rounded-md px-2 py-1.5 text-left text-sm",
                      cluster.id === selectedId
                        ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
                        : "hover:bg-slate-100 dark:hover:bg-slate-800",
                    )}
                    onClick={() => {
                      selectCluster(cluster);
                    }}
                  >
                    <span className="flex w-full items-center justify-between gap-2">
                      <span className="font-medium">{cluster.name}</span>
                      <StatusBadge status={cluster.status} />
                    </span>
                    <span className="truncate text-[10px] text-slate-600 dark:text-slate-400">{cluster.apiUrl}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="flex-1 overflow-y-auto">
          {!editing ? (
            <EmptyState title="Select a cluster" description="Pick one from the list, or attach a new one." />
          ) : (
            <form
              className="flex max-w-xl flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                void handleSave();
              }}
            >
              <div>
                <label htmlFor="fed-cluster-name" className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  Name
                </label>
                <input
                  id="fed-cluster-name"
                  type="text"
                  className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                  value={form.name}
                  onChange={(e) => {
                    setForm({ ...form, name: e.target.value });
                  }}
                />
              </div>

              <div>
                <label htmlFor="fed-cluster-api-url" className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  API URL
                </label>
                <input
                  id="fed-cluster-api-url"
                  type="text"
                  placeholder="https://pve2.example.com:8006"
                  className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                  value={form.apiUrl}
                  onChange={(e) => {
                    setForm({ ...form, apiUrl: e.target.value });
                  }}
                />
              </div>

              <fieldset className="space-y-2 rounded-md border border-slate-200 p-3 dark:border-slate-700">
                <legend className="px-1 text-xs font-medium text-slate-600 dark:text-slate-300">
                  Credential {!creating && !form.credentialTouched ? "(configured — leave blank to keep)" : ""}
                </legend>

                <div className="flex gap-3 text-sm">
                  <label className="flex items-center gap-1.5">
                    <input
                      type="radio"
                      name="fed-credential-kind"
                      checked={form.credentialKind === "ticket"}
                      onChange={() => {
                        setForm({ ...form, credentialKind: "ticket", credentialTouched: true });
                      }}
                    />
                    Username / password
                  </label>
                  <label className="flex items-center gap-1.5">
                    <input
                      type="radio"
                      name="fed-credential-kind"
                      checked={form.credentialKind === "token"}
                      onChange={() => {
                        setForm({ ...form, credentialKind: "token", credentialTouched: true });
                      }}
                    />
                    API token
                  </label>
                </div>

                {form.credentialKind === "ticket" ? (
                  <>
                    <div>
                      <label htmlFor="fed-cluster-username" className="text-xs text-slate-600 dark:text-slate-400">
                        Username
                      </label>
                      <input
                        id="fed-cluster-username"
                        type="text"
                        autoComplete="off"
                        className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                        value={form.username}
                        onChange={(e) => {
                          setForm({ ...form, username: e.target.value, credentialTouched: true });
                        }}
                      />
                    </div>
                    <div>
                      <label htmlFor="fed-cluster-password" className="text-xs text-slate-600 dark:text-slate-400">
                        Password
                      </label>
                      <input
                        id="fed-cluster-password"
                        type="password"
                        autoComplete="new-password"
                        className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                        value={form.password}
                        onChange={(e) => {
                          setForm({ ...form, password: e.target.value, credentialTouched: true });
                        }}
                      />
                    </div>
                    <div>
                      <label htmlFor="fed-cluster-realm" className="text-xs text-slate-600 dark:text-slate-400">
                        Realm (optional)
                      </label>
                      <input
                        id="fed-cluster-realm"
                        type="text"
                        placeholder="pam"
                        className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                        value={form.realm}
                        onChange={(e) => {
                          setForm({ ...form, realm: e.target.value, credentialTouched: true });
                        }}
                      />
                    </div>
                  </>
                ) : (
                  <div>
                    <label htmlFor="fed-cluster-token" className="text-xs text-slate-600 dark:text-slate-400">
                      Token
                    </label>
                    <input
                      id="fed-cluster-token"
                      type="password"
                      autoComplete="off"
                      placeholder="user@realm!tokenid=secret"
                      className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                      value={form.token}
                      onChange={(e) => {
                        setForm({ ...form, token: e.target.value, credentialTouched: true });
                      }}
                    />
                  </div>
                )}
              </fieldset>

              {selected && (
                <div>
                  <h3 className="text-xs font-medium text-slate-600 dark:text-slate-300">WireGuard tunnel link</h3>
                  <div className="mt-1">
                    <TunnelLinkage
                      cluster={selected}
                      canWrite={canWrite}
                      disabledReason={writeDisabledReason}
                      clearing={updateMutation.isPending}
                      onClear={() => {
                        void handleClearTunnelOverride(selected);
                      }}
                    />
                  </div>
                </div>
              )}

              {validationError && <p className="text-xs text-red-600 dark:text-red-400">{validationError}</p>}

              <div className="flex flex-wrap gap-2">
                <Tooltip content={writeDisabledReason}>
                  <span>
                    <Button type="submit" variant="primary" disabled={!canWrite || !!validationError}>
                      {creating ? "Attach" : "Save"}
                    </Button>
                  </span>
                </Tooltip>
                <Button type="button" variant="secondary" onClick={cancelEdit}>
                  Cancel
                </Button>
                {selected && (
                  <Tooltip content={writeDisabledReason}>
                    <span>
                      <Button
                        type="button"
                        variant="destructive"
                        disabled={!canWrite}
                        onClick={() => {
                          setDetachTarget(selected);
                        }}
                      >
                        Detach
                      </Button>
                    </span>
                  </Tooltip>
                )}
              </div>
            </form>
          )}
        </div>
      </div>

      <Dialog
        open={detachTarget !== undefined}
        onOpenChange={(open) => {
          if (!open) setDetachTarget(undefined);
        }}
      >
        <DialogContent>
          <DialogTitle>Detach {detachTarget?.name}?</DialogTitle>
          <DialogDescription>
            This only removes vnprox's own registration row — it never touches the attached cluster's own PVE
            config. You lose:
          </DialogDescription>
          <ul className="mt-2 list-inside list-disc text-sm text-slate-600 dark:text-slate-300">
            <li>Its capsule in the aggregated global topology view</li>
            <li>Cross-cluster IPAM conflict detection against this cluster's subnets</li>
            <li>Its rows in global audit/search until it is attached again</li>
          </ul>
          <p className="mt-2 text-xs text-slate-600 dark:text-slate-400">
            Detaching is cheap to do again from scratch, but you will need the credential again to reattach.
          </p>
          <div className="mt-5 flex justify-end gap-2">
            <Button
              variant="secondary"
              onClick={() => {
                setDetachTarget(undefined);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() => {
                void handleConfirmDetach();
              }}
            >
              Detach
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
