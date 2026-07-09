import clsx from "clsx";
import type { Layer } from "../api/types";

const LAYER_LABELS: Record<Layer, string> = { phys: "Physical", l2: "L2", sdn: "SDN", guest: "Guests" };
const LAYER_KEYS: Record<Layer, string> = { phys: "1", l2: "2", sdn: "3", guest: "4" };

export interface LayerToggleBarProps {
  activeLayers: ReadonlySet<Layer>;
  onToggle: (layer: Layer) => void;
  layerOrder: readonly Layer[];
}

/** The `1`-`4` layer toggle rail (docs/features/topology.md §1). */
export function LayerToggleBar({ activeLayers, onToggle, layerOrder }: LayerToggleBarProps) {
  return (
    <div className="flex gap-1 rounded-md border border-slate-200 bg-white/90 p-1 shadow-sm dark:border-slate-700 dark:bg-slate-900/90">
      {layerOrder.map((layer) => {
        const active = activeLayers.has(layer);
        return (
          <button
            key={layer}
            type="button"
            aria-pressed={active}
            onClick={() => {
              onToggle(layer);
            }}
            className={clsx(
              "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
              active
                ? "bg-accent-600 text-white"
                : "text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800",
            )}
          >
            <kbd className="rounded border border-current/30 px-1 text-[10px] opacity-70">{LAYER_KEYS[layer]}</kbd>
            {LAYER_LABELS[layer]}
          </button>
        );
      })}
    </div>
  );
}
