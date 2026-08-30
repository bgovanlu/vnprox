// SPDX-License-Identifier: Apache-2.0

import clsx from "clsx";
import type { Layer } from "../api/types";

const LAYER_LABELS: Record<Layer, string> = { phys: "Physical", l2: "L2", sdn: "SDN", guest: "Guests" };
const LAYER_KEYS: Record<Layer, string> = { phys: "1", l2: "2", sdn: "3", guest: "4" };

export interface LayerToggleBarProps {
  activeLayers: ReadonlySet<Layer>;
  onToggle: (layer: Layer) => void;
  layerOrder: readonly Layer[];
  /** T-1003: an additional "Flows" toggle rendered after the four base
   * layers — distinct from `activeLayers`/`onToggle` because it isn't one
   * of the server-emitted entity layers `Layer` enumerates (docs/api.md's
   * `GET /topology` node.layer values); it's a client-only map-painting
   * overlay, the same kind of per-session view toggle `trafficMode` already
   * is. Both this prop and `onToggleFlows` must be provided together to
   * render the button at all — omitted (every pre-T-1003 call site) keeps
   * this bar's exact previous 4-button-only output. */
  flowsLayerActive?: boolean;
  onToggleFlows?: () => void;
  /** T-1303: a 6th "Latency" toggle, same optional-pair convention as
   * flowsLayerActive/onToggleFlows above — a client-only heatmap-painting
   * overlay (topology/latencyMode.ts), not one of the server-emitted
   * entity layers either. Both props must be provided together to render
   * the button; omitted keeps this bar's exact prior output. */
  latencyLayerActive?: boolean;
  onToggleLatency?: () => void;
  /** T-1306: a 7th "MTU" toggle, same optional-pair convention as
   * latencyLayerActive/onToggleLatency above — a client-only verified-MTU
   * badge overlay (topology/mtuOverlay.ts), not one of the server-emitted
   * entity layers either. Both props must be provided together to render
   * the button; omitted keeps this bar's exact prior output. */
  mtuLayerActive?: boolean;
  onToggleMTU?: () => void;
  /** T-1402: an 8th "WireGuard" toggle, same optional-pair convention as
   * mtuLayerActive/onToggleMTU above — a client-only overlay rendering
   * every visible tunnel as a map edge (topology/../wireguard/
   * wgTunnelEdges.ts), not one of the server-emitted entity layers either.
   * Both props must be provided together to render the button; omitted
   * keeps this bar's exact prior output. */
  wgLayerActive?: boolean;
  onToggleWG?: () => void;
  /** T-1503: a 9th "Ceph" toggle, same optional-pair convention as
   * wgLayerActive/onToggleWG above — a client-only overlay painting
   * ceph-public/ceph-cluster badges onto existing bond/PhysNic map nodes
   * (topology/cephOverlay.ts), not one of the server-emitted entity layers
   * either. Both props must be provided together to render the button;
   * omitted keeps this bar's exact prior output. */
  cephLayerActive?: boolean;
  onToggleCeph?: () => void;
  /** T-1502: a 10th "Kubernetes" toggle, same optional-pair convention as
   * wgLayerActive/onToggleWG above — a client-only overlay rendering
   * T-1501's pod/service CIDR model as map regions plus node<->guest
   * correlation lines (topology/layers/k8sOverlay.ts), not one of the
   * server-emitted entity layers either. Both props must be provided
   * together to render the button; omitted keeps this bar's exact prior
   * output. */
  k8sLayerActive?: boolean;
  onToggleK8s?: () => void;
  /** T-3908: an 11th "Recency" toggle, same optional-pair convention as
   * k8sLayerActive/onToggleK8s above — a client-only overlay painting each
   * entity's config-change recency as a corner badge
   * (topology/recencyOverlay.ts), not one of the server-emitted entity
   * layers either. Both props must be provided together to render the
   * button; omitted keeps this bar's exact prior output. */
  recencyLayerActive?: boolean;
  onToggleRecency?: () => void;
  /** T-3910: a 12th "Replay" toggle, same optional-pair convention as
   * recencyLayerActive/onToggleRecency above — a client-only animate/scrub
   * control over the map's existing traffic-heat/flow-path paint at a
   * chosen past instant (topology/replay/FlowReplayPanel.tsx), not one of
   * the server-emitted entity layers either, and deliberately a separate
   * toggle from the always-visible History scrubber so the two read as
   * distinct tools. Both props must be provided together to render the
   * button; omitted keeps this bar's exact prior output. */
  replayLayerActive?: boolean;
  onToggleReplay?: () => void;
}

/** The `1`-`4` layer toggle rail (docs/features/topology.md §1), plus an
 * optional 5th "Flows" overlay toggle (T-1003) appended after it. */
export function LayerToggleBar({
  activeLayers,
  onToggle,
  layerOrder,
  flowsLayerActive,
  onToggleFlows,
  latencyLayerActive,
  onToggleLatency,
  mtuLayerActive,
  onToggleMTU,
  wgLayerActive,
  onToggleWG,
  cephLayerActive,
  onToggleCeph,
  k8sLayerActive,
  onToggleK8s,
  recencyLayerActive,
  onToggleRecency,
  replayLayerActive,
  onToggleReplay,
}: LayerToggleBarProps) {
  return (
    <div className="flex gap-1 rounded-md border border-border bg-white/90 p-1 shadow-sm dark:bg-slate-900/90">
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
                : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
            )}
          >
            <kbd className="rounded border border-current/30 px-1 text-[10px] opacity-70">{LAYER_KEYS[layer]}</kbd>
            {LAYER_LABELS[layer]}
          </button>
        );
      })}
      {onToggleFlows && (
        <button
          type="button"
          aria-pressed={flowsLayerActive ?? false}
          onClick={onToggleFlows}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            flowsLayerActive
              ? "bg-cyan-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Flows
        </button>
      )}
      {onToggleLatency && (
        <button
          type="button"
          aria-pressed={latencyLayerActive ?? false}
          onClick={onToggleLatency}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            latencyLayerActive
              ? "bg-fuchsia-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Latency
        </button>
      )}
      {onToggleMTU && (
        <button
          type="button"
          aria-pressed={mtuLayerActive ?? false}
          onClick={onToggleMTU}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            mtuLayerActive
              ? "bg-teal-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          MTU
        </button>
      )}
      {onToggleWG && (
        <button
          type="button"
          aria-pressed={wgLayerActive ?? false}
          onClick={onToggleWG}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            wgLayerActive
              // T-4201: was a raw `bg-indigo-600` while its sibling toggle
              // above used `bg-accent-600`. The two were byte-identical on
              // screen for as long as the accent alias pointed at indigo,
              // so the divergence was invisible; re-pointing the accent to
              // signal azure is what made one active toggle in this bar
              // paint a different colour from the other.
              ? "bg-accent-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          WireGuard
        </button>
      )}
      {onToggleCeph && (
        <button
          type="button"
          aria-pressed={cephLayerActive ?? false}
          onClick={onToggleCeph}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            cephLayerActive
              ? "bg-orange-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Ceph
        </button>
      )}
      {onToggleK8s && (
        <button
          type="button"
          aria-pressed={k8sLayerActive ?? false}
          onClick={onToggleK8s}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            k8sLayerActive
              ? "bg-emerald-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Kubernetes
        </button>
      )}
      {onToggleRecency && (
        <button
          type="button"
          aria-pressed={recencyLayerActive ?? false}
          onClick={onToggleRecency}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            recencyLayerActive
              ? "bg-amber-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Recency
        </button>
      )}
      {onToggleReplay && (
        <button
          type="button"
          aria-pressed={replayLayerActive ?? false}
          onClick={onToggleReplay}
          className={clsx(
            "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
            replayLayerActive
              ? "bg-lime-600 text-white"
              : "text-fg-subtle hover:bg-slate-100 dark:hover:bg-slate-800",
          )}
        >
          Replay
        </button>
      )}
    </div>
  );
}
