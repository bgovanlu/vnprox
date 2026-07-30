// The "virtual switch" view: the same GET /topology graph the elk canvas
// renders, re-drawn as a stack of switch faceplates per cluster node
// (docs/features/topology.md §2). Pure/presentational — it receives an
// already-built SwitchTopology (TopologyPage runs buildSwitchModel over the
// same merged nodes/edges the graph view uses, so guest-group expansion,
// staleness, and WS deltas all flow through unchanged) plus the shared UI
// state (selection, VLAN filter, layer toggles) and two callbacks.
import { EmptyState } from "../components/EmptyState";
import type { Layer } from "../api/types";
import { SwitchFaceplate } from "./SwitchFaceplate";
import { switchCarriesVlan, type SwitchTopology, type SwitchUplink } from "./switchModel";

const BOND_KINDS = new Set(["bond", "ovs-bond"]);

export interface SwitchViewProps {
  topology: SwitchTopology;
  selectedId: string | undefined;
  vlanFilter: number | undefined;
  activeLayers: ReadonlySet<Layer>;
  staleNodeGroups: ReadonlySet<string>;
  onSelect: (ref: string) => void;
  onExpandGroup: (groupId: string) => void;
}

/** A lone free port rendered outside any switch (an unattached NIC/bond) —
 * reuses the same look as an uplink module so it reads consistently. */
function FreePort({
  port,
  onSelect,
  onExpandGroup,
}: {
  port: SwitchUplink;
  onSelect: (ref: string) => void;
  onExpandGroup: (groupId: string) => void;
}) {
  // T-1907: a phys-group pill can end up here too, when every one of its
  // collapsed NICs was genuinely unattached (no bond/bridge edge survived
  // to synthesize a group edge) — same dashed "click to expand" affordance
  // SwitchFaceplate.tsx's own NicPort gives it inside an uplink bay.
  if (port.isGroup) {
    return (
      <button
        type="button"
        aria-label={`Expand ${String(port.count ?? "")} collapsed NICs`}
        data-entity-ref={port.ref}
        onClick={() => {
          onExpandGroup(port.ref);
        }}
        className="flex items-center gap-1.5 rounded border border-dashed border-slate-400 bg-slate-100 px-2 py-1 text-xs text-slate-600 hover:border-slate-600 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300"
      >
        <span className="h-2 w-2 rounded-full bg-slate-400" />
        <span className="font-mono">{port.label}</span>
        <span className="text-[9px] uppercase tracking-wide">click to expand</span>
      </button>
    );
  }
  const isBond = BOND_KINDS.has(port.kind);
  return (
    <button
      type="button"
      aria-label={port.label}
      data-entity-ref={port.ref}
      onClick={() => {
        onSelect(port.ref);
      }}
      className="flex items-center gap-1.5 rounded border border-slate-300 bg-white px-2 py-1 text-xs hover:border-accent-500 dark:border-slate-600 dark:bg-slate-900"
    >
      <span
        className={
          port.status === "down"
            ? "h-2 w-2 rounded-full bg-red-500"
            : port.status === "unknown"
              ? "h-2 w-2 rounded-full bg-slate-400"
              : "h-2 w-2 rounded-full bg-emerald-500"
        }
      />
      <span className="font-mono text-slate-700 dark:text-slate-200">{port.label}</span>
      <span className="text-[10px] uppercase tracking-wide text-slate-400">{isBond ? "bond" : "nic"}</span>
    </button>
  );
}

export function SwitchView({
  topology,
  selectedId,
  vlanFilter,
  activeLayers,
  staleNodeGroups,
  onSelect,
  onExpandGroup,
}: SwitchViewProps) {
  const totalSwitches = topology.nodes.reduce((sum, n) => sum + n.switches.length, 0);
  if (totalSwitches === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          title="No bridges to show"
          description="No Linux or OVS bridges were discovered. Switch to the Graph view to see NICs, LLDP neighbors, and SDN entities on their own."
        />
      </div>
    );
  }

  // A single filter decision, applied consistently to whole switches and to
  // individual VLAN-bearing slots (access ports, VNets, sub-interfaces).
  const dimVid = (vid: number | undefined): boolean => vlanFilter !== undefined && vid !== vlanFilter;

  return (
    <div className="h-full overflow-auto p-3">
      <div className="flex flex-col gap-6">
        {topology.nodes.map((group) => {
          const stale = staleNodeGroups.has(group.node);
          return (
            <section key={group.node} aria-label={`node ${group.node}`}>
              <div className="mb-2 flex items-center gap-2">
                <h2 className="font-mono text-sm font-semibold text-slate-700 dark:text-slate-200">{group.node}</h2>
                <span className="text-xs text-slate-400 dark:text-slate-400">
                  {group.switches.length} switch{group.switches.length === 1 ? "" : "es"}
                </span>
                {stale && (
                  <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium uppercase text-amber-700 dark:bg-amber-950 dark:text-amber-300">
                    stale
                  </span>
                )}
              </div>
              <div className="grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                {group.switches.map((sw) => (
                  <SwitchFaceplate
                    key={sw.ref}
                    model={sw}
                    selectedId={selectedId}
                    showUplinks={activeLayers.has("phys")}
                    showVlans={activeLayers.has("l2")}
                    showPorts={activeLayers.has("guest")}
                    showVnets={activeLayers.has("sdn")}
                    dimmed={vlanFilter !== undefined && !switchCarriesVlan(sw, vlanFilter)}
                    dimVid={dimVid}
                    stale={stale}
                    onSelect={onSelect}
                    onExpandGroup={onExpandGroup}
                  />
                ))}
              </div>
              {activeLayers.has("phys") && group.freePorts.length > 0 && (
                <div className="mt-2 rounded-lg border border-dashed border-slate-300 p-2 dark:border-slate-700">
                  <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-400">
                    Unattached ports
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {group.freePorts.map((p) => (
                      <FreePort key={p.ref} port={p} onSelect={onSelect} onExpandGroup={onExpandGroup} />
                    ))}
                  </div>
                </div>
              )}
            </section>
          );
        })}
      </div>
    </div>
  );
}
