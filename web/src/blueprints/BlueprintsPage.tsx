// T-603's blueprint list/detail/param-form page: browse the five bundled
// starters plus any saved blueprints, view a preview diagram + param form,
// instantiate (-> opens the resulting draft in the changeset drawer for
// the normal review/apply flow), capture-from-node, and import/export
// files (docs/features/blueprints.md §1).
import { useRef, useState } from "react";
import clsx from "clsx";
import type { Blueprint, BlueprintParamValue } from "../api/types";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import { EmptyState } from "../components/EmptyState";
import { BlueprintPreviewDiagram } from "./BlueprintPreviewDiagram";
import { ParamForm } from "./ParamForm";
import {
  useBlueprintsQuery,
  useCaptureBlueprintMutation,
  useDeleteBlueprintMutation,
  useInstantiateBlueprintMutation,
  useSaveBlueprintMutation,
} from "./queries";

function parseNodes(raw: string): string[] | undefined {
  const parts = raw
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  return parts.length > 0 ? parts : undefined;
}

export function BlueprintsPage() {
  const { data, isLoading, error } = useBlueprintsQuery();
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [nodesValue, setNodesValue] = useState("");
  const [captureNode, setCaptureNode] = useState("");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const saveMutation = useSaveBlueprintMutation();
  const deleteMutation = useDeleteBlueprintMutation();
  const captureMutation = useCaptureBlueprintMutation();
  const instantiateMutation = useInstantiateBlueprintMutation();
  const setActiveChangesetId = useChangesetDrawerStore((s) => s.setActiveId);
  const { toast } = useToast();

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

  async function handleImportFile(file: File): Promise<void> {
    try {
      const text = await file.text();
      const parsed = JSON.parse(text) as Blueprint;
      // Import always creates a *new* saved blueprint (never silently
      // overwrites an existing one, and never a starter id).
      const toSave: Blueprint = { ...parsed, id: "", readOnly: false };
      const saved = await saveMutation.mutateAsync(toSave);
      setSelectedId(saved.id);
      toast({ title: "Imported", description: saved.name, variant: "success" });
    } catch {
      toast({ title: "Could not import file", description: "Not a valid blueprint JSON file.", variant: "error" });
    }
  }

  if (isLoading) {
    return <EmptyState title="Loading…" description="Fetching blueprints." />;
  }
  if (error) {
    return <EmptyState title="Could not load blueprints" description="Check your connection and try again." />;
  }

  return (
    <div className="flex h-full gap-4 overflow-hidden p-4">
      <div className="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">Blueprints</h2>
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
            <button
              type="button"
              className="rounded-md border border-slate-300 px-2 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
              onClick={() => fileInputRef.current?.click()}
            >
              Import
            </button>
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
                {bp.readOnly ? <span className="text-[10px] uppercase tracking-wide text-slate-400">starter</span> : null}
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
            <button
              type="button"
              className="shrink-0 rounded-md border border-slate-300 px-2 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
              onClick={() => {
                void handleCapture();
              }}
            >
              Capture
            </button>
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
                <h1 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{selected.name}</h1>
                {selected.description ? (
                  <p className="max-w-2xl text-sm text-slate-500 dark:text-slate-400">{selected.description}</p>
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
                  <button
                    type="button"
                    className="rounded-md border border-red-300 px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                    onClick={() => {
                      void handleDelete(selected);
                    }}
                  >
                    Delete
                  </button>
                ) : null}
              </div>
            </div>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">Preview</h3>
              <BlueprintPreviewDiagram blueprint={selected} />
            </section>

            <section>
              <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">Instantiate</h3>
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
              />
            </section>
          </div>
        )}
      </div>
    </div>
  );
}
