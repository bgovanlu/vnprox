// SPDX-License-Identifier: Apache-2.0

// A lightweight, framework-free-layout preview diagram (docs/features/
// blueprints.md §1: "Each starter carries ... a preview diagram", T-603's
// "wizard-preview machinery" — since no SDN-wizard preview canvas exists
// yet to reuse (T-403 not landed on this branch's base; see the T-603
// report), this renders a simple layered box-and-line diagram directly
// from preview.ts's pure {nodes, edges} rather than pulling in the full
// @xyflow/react topology canvas for a static, non-interactive preview).
import { layerOf, buildPreview } from "./preview";
import type { Blueprint } from "../api/types";

const DEFAULT_COLOR =
  "border-slate-300 bg-slate-100 text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300";

const KIND_COLORS: Record<string, string> = {
  input: DEFAULT_COLOR,
  bond: "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200",
  bridge: "border-sky-300 bg-sky-50 text-sky-800 dark:border-sky-700 dark:bg-sky-950 dark:text-sky-200",
  vlan: "border-violet-300 bg-violet-50 text-violet-800 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-200",
  "sdn-zone": "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
  "sdn-vnet": "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
  "sdn-subnet": "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
};

export function BlueprintPreviewDiagram({ blueprint }: { blueprint: Blueprint }) {
  const preview = buildPreview(blueprint);
  const layers = new Map<number, typeof preview.nodes>();
  for (const node of preview.nodes) {
    const layer = layerOf(node.kind);
    const bucket = layers.get(layer) ?? [];
    bucket.push(node);
    layers.set(layer, bucket);
  }
  const layerIndices = [...layers.keys()].sort((a, b) => a - b);

  if (preview.nodes.length === 0) {
    return <p className="text-sm text-slate-500 dark:text-slate-400">No entities to preview.</p>;
  }

  return (
    <div data-testid="blueprint-preview-diagram" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start gap-6">
        {layerIndices.map((layerIdx) => (
          <div key={layerIdx} className="flex flex-col gap-2">
            {(layers.get(layerIdx) ?? []).map((node) => (
              <div
                key={node.id}
                className={`rounded-md border px-2 py-1 text-xs font-medium ${KIND_COLORS[node.kind] ?? DEFAULT_COLOR}`}
                title={node.kind}
              >
                {node.label}
              </div>
            ))}
          </div>
        ))}
      </div>
      <ul className="text-xs text-slate-500 dark:text-slate-400">
        {preview.edges.map((e) => (
          <li key={`${e.from}->${e.to}`}>
            {e.from} → {e.to}
          </li>
        ))}
      </ul>
    </div>
  );
}
