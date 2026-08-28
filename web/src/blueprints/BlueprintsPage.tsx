// SPDX-License-Identifier: Apache-2.0

// T-603's blueprint list/detail/param-form page: browse the five bundled
// starters plus any saved blueprints, view a preview diagram + param form,
// instantiate (-> opens the resulting draft in the changeset drawer for
// the normal review/apply flow), capture-from-node, and import/export
// files (docs/features/blueprints.md §1).
import { useRef, useState } from "react";
import clsx from "clsx";
import type { Blueprint, BlueprintBundle, BlueprintParamValue } from "../api/types";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Tooltip } from "../components/Tooltip";
import { BlueprintImportDialog } from "./BlueprintImportDialog";
import { BlueprintPreviewDiagram } from "./BlueprintPreviewDiagram";
import { ParamForm } from "./ParamForm";
import {
  useBlueprintsQuery,
  useCaptureBlueprintMutation,
  useDeleteBlueprintMutation,
  useInstantiateBlueprintMutation,
  useSaveBlueprintMutation,
} from "./queries";

/** A file the user picked may be either T-1107's bundle envelope
 * (`{bundleVersion, blueprint, signature?}`) or a legacy plain-Blueprint
 * export (docs/api.md's pre-T-1107 "file-level import" convention: just
 * `GET /blueprints/{id}`'s JSON body). Both are normalized to a
 * BlueprintBundle here so BlueprintImportDialog only ever has one shape to
 * handle — a legacy file is treated exactly like an unsigned bundle. */
function toBundle(parsed: unknown): BlueprintBundle | null {
  if (typeof parsed !== "object" || parsed === null) return null;
  const obj = parsed as Record<string, unknown>;
  if ("bundleVersion" in obj && "blueprint" in obj) {
    return obj as unknown as BlueprintBundle;
  }
  if ("blueprintVersion" in obj && "entities" in obj) {
    return { bundleVersion: 1, blueprint: obj as unknown as Blueprint };
  }
  return null;
}

function parseNodes(raw: string): string[] | undefined {
  const parts = raw
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  return parts.length > 0 ? parts : undefined;
}

export function BlueprintsPage() {
  const { data, isLoading, error, refetch } = useBlueprintsQuery();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [nodesValue, setNodesValue] = useState("");
  const [captureNode, setCaptureNode] = useState("");
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [importBundle, setImportBundle] = useState<BlueprintBundle | null>(null);
  const [importDialogOpen, setImportDialogOpen] = useState(false);

  const saveMutation = useSaveBlueprintMutation();
  const deleteMutation = useDeleteBlueprintMutation();
  const captureMutation = useCaptureBlueprintMutation();
  const instantiateMutation = useInstantiateBlueprintMutation();
  const setActiveChangesetId = useChangesetDrawerStore((s) => s.setActiveId);
  const { toast } = useToast();
  const { data: session } = useSession();
  // Every write here (import/capture/delete/instantiate) ultimately lands
  // as bridge/bond/vlan/SDN change ops or a new saved blueprint, none of it
  // scoped to one specific node the way an entity editor's edits are — so
  // this is gated the same way T-605's onboarding walkthrough gates its own
  // cluster-wide writes: hasAnyCap(session, "netWrite"), disabled-with-
  // tooltip rather than hidden (docs/user-guide.md §5), never a second
  // gating mechanism.
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  const items = data?.items ?? [];
  const selected = items.find((b) => b.id === selectedId);

  async function handleInstantiate(params: Record<string, BlueprintParamValue>): Promise<void> {
    if (!selected) return;
    try {
      const changeset = await instantiateMutation.mutateAsync({
        id: selected.id,
        req: { params, nodes: parseNodes(nodesValue) },
      });
      setActiveChangesetId(changeset.id);
      toast({
        title: "Changeset drafted",
        description: `${String(changeset.ops.length)} op(s) — review before applying.`,
        variant: "success",
      });
    } catch {
      toast({ title: "Could not instantiate blueprint", variant: "error" });
    }
  }

  async function handleCapture(): Promise<void> {
    if (!captureNode.trim()) return;
    try {
      const captured = await captureMutation.mutateAsync({ node: captureNode.trim() });
      const saved = await saveMutation.mutateAsync(captured);
      setSelectedId(saved.id);
      toast({ title: "Captured and saved", description: saved.name, variant: "success" });
    } catch {
      toast({ title: "Could not capture from node", variant: "error" });
    }
  }

  async function handleDelete(bp: Blueprint): Promise<void> {
    try {
      await deleteMutation.mutateAsync(bp.id);
      if (selectedId === bp.id) setSelectedId(undefined);
      toast({ title: "Blueprint deleted", variant: "success" });
    } catch {
      toast({ title: "Could not delete blueprint", variant: "error" });
    }
  }

  function handleExport(bp: Blueprint): void {
    const blob = new Blob([JSON.stringify(bp, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${bp.id || bp.name}.blueprint.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  // T-1107: import now always goes through BlueprintImportDialog's
  // signature-verification/trust-decision flow (POST /blueprints/import),
  // never straight to POST /blueprints — even a legacy plain-Blueprint file
  // (no bundleVersion/signature) is normalized to an unsigned bundle and
  // gated behind that dialog's "I trust this file" step, so there is one
  // import path, not two.
  async function handleImportFile(file: File): Promise<void> {
    try {
      const text = await file.text();
      const parsed: unknown = JSON.parse(text);
      const bundle = toBundle(parsed);
      if (!bundle) {
        throw new Error("not a blueprint bundle or blueprint file");
      }
      setImportBundle(bundle);
      setImportDialogOpen(true);
    } catch {
      toast({ title: "Could not import file", description: "Not a valid blueprint bundle file.", variant: "error" });
    }
  }

  if (isLoading) {
    return <EmptyState title="Loading…" description="Fetching blueprints." />;
  }
  if (error) {
    return (
      <EmptyState
        icon="node"
        variant="failed"
        title="Could not load blueprints"
        description="Check your connection and try again."
        action={
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <PageHeader title="Blueprints" />
      <div className="flex min-h-0 flex-1 gap-4 overflow-hidden">
      <div className="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">Saved</h2>
          <div>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (file) void handleImportFile(file);
                e.target.value = "";
              }}
            />
            <Tooltip content={writeDisabledReason}>
              <span>
                <button
                  type="button"
                  disabled={!canWrite}
                  className="rounded-md border border-slate-300 px-2 py-1 text-xs font-medium hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-800"
                  onClick={() => fileInputRef.current?.click()}
                >
                  Import
                </button>
              </span>
            </Tooltip>
          </div>
        </div>

        <ul className="flex flex-col gap-1" data-testid="blueprint-list">
          {items.map((bp) => (
            <li key={bp.id}>
              <button
                type="button"
                className={clsx(
                  "flex w-full flex-col items-start rounded-md px-2 py-1.5 text-left text-sm",
                  bp.id === selectedId
                    ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
                    : "hover:bg-slate-100 dark:hover:bg-slate-800",
                )}
                onClick={() => {
                  setSelectedId(bp.id);
                }}
              >
                <span className="font-medium">{bp.name}</span>
                {bp.readOnly ? (
                  <span className="text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-400">starter</span>
                ) : null}
              </button>
            </li>
          ))}
        </ul>

        <div className="mt-auto flex flex-col gap-2 border-t border-slate-200 pt-3 dark:border-slate-800">
          <label htmlFor="capture-node" className="text-xs font-medium text-slate-600 dark:text-slate-300">
            Capture from node
          </label>
          <div className="flex gap-2">
            <input
              id="capture-node"
              type="text"
              placeholder="pve1"
              className="w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
              value={captureNode}
              onChange={(e) => {
                setCaptureNode(e.target.value);
              }}
            />
            <Tooltip content={writeDisabledReason}>
              <span>
                <button
                  type="button"
                  disabled={!canWrite}
                  className="shrink-0 rounded-md border border-slate-300 px-2 py-1 text-xs font-medium hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-800"
                  onClick={() => {
                    void handleCapture();
                  }}
                >
                  Capture
                </button>
              </span>
            </Tooltip>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto">
        {!selected ? (
          <EmptyState title="Select a blueprint" description="Pick one from the list to preview and instantiate it." />
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex items-start justify-between">
              <div>
                {/* T-3404: PageHeader above now owns the page's one <h1>
                 * ("Blueprints") — the selected blueprint's own name drops
                 * to h2, the same level GuestsPage/etc. use for a
                 * detail-panel's own heading below the page title. */}
                <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{selected.name}</h2>
                {selected.description ? (
                  <p className="max-w-2xl text-sm text-slate-600 dark:text-slate-400">{selected.description}</p>
                ) : null}
              </div>
              <div className="flex gap-2">
                <button
                  type="button"
                  className="rounded-md border border-slate-300 px-2 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
                  onClick={() => {
                    handleExport(selected);
                  }}
                >
                  Export
                </button>
                {!selected.readOnly ? (
                  <Tooltip content={writeDisabledReason}>
                    <span>
                      <button
                        type="button"
                        disabled={!canWrite}
                        className="rounded-md border border-red-300 px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                        onClick={() => {
                          void handleDelete(selected);
                        }}
                      >
                        Delete
                      </button>
                    </span>
                  </Tooltip>
                ) : null}
              </div>
            </div>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-400">Preview</h3>
              <BlueprintPreviewDiagram blueprint={selected} />
            </section>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-600 dark:text-slate-400">Instantiate</h3>
              <ParamForm
                key={selected.id}
                blueprintId={selected.id}
                params={selected.params}
                nodesValue={nodesValue}
                onNodesChange={setNodesValue}
                onValidSubmit={(params) => {
                  void handleInstantiate(params);
                }}
                submitting={instantiateMutation.isPending}
                submitDisabledReason={writeDisabledReason}
              />
            </section>
          </div>
        )}
      </div>
      </div>

      <BlueprintImportDialog
        open={importDialogOpen}
        onOpenChange={setImportDialogOpen}
        bundle={importBundle}
        onImported={(bp) => {
          setSelectedId(bp.id);
        }}
      />
    </div>
  );
}
