// SDN cockpit: zone -> vnet -> subnet tree with per-node realization status
// plus a detail panel rendering the staged-vs-running pending diff
// (docs/features/sdn.md §1). Read-only for T-401 — the zone wizards
// (docs/features/sdn.md §2) and editors land with T-402/T-403.
import { useEffect, useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import type { SdnSubnet, SdnTree, SdnVnet, SdnZone } from "../api/types";
import { SdnEditorLauncher } from "./SdnEditorLauncher";
import { EvpnView } from "./EvpnView";
import { InSyncBadge, PendingDiffView } from "./PendingDiffView";
import { useSdnEditorStore } from "./sdnEditorStore";
import { StatusDot } from "./StatusDot";
import { sdnNodeEntityStatus, sdnZoneEntityStatus } from "./status";
import { firstSelection, resolveSdnSelection, type SdnSelection } from "./tree";
import { useSdnQuery } from "./queries";

type SdnTab = "configuration" | "evpn";

function TreeRow({
  depth,
  label,
  status,
  pending,
  selected,
  onClick,
}: {
  depth: number;
  label: string;
  status: ReturnType<typeof sdnZoneEntityStatus>;
  pending?: string;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(
        "flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm transition-colors",
        selected
          ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
          : "text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800/60",
      )}
      style={{ paddingLeft: `${String(0.5 + depth * 1.25)}rem` }}
    >
      <StatusDot status={status} />
      <span className="truncate">{label}</span>
      {pending && (
        <span className="ml-auto shrink-0 rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900 dark:text-amber-300">
          {pending}
        </span>
      )}
    </button>
  );
}

function zoneLabel(z: SdnZone): string {
  return `${z.id} (${z.type})`;
}

function vnetLabel(v: SdnVnet): string {
  return v.alias ? `${v.id} — ${v.alias}` : v.id;
}

function subnetLabel(s: SdnSubnet): string {
  return s.cidr || s.id;
}

function SdnTreeView({
  tree,
  selection,
  onSelect,
}: {
  tree: SdnTree;
  selection: SdnSelection | undefined;
  onSelect: (s: SdnSelection) => void;
}) {
  return (
    <div className="flex flex-col gap-0.5 overflow-y-auto" role="tree" aria-label="SDN zones">
      {tree.zones.map((zone) => {
        const zoneSel: SdnSelection = { kind: "zone", zoneId: zone.id };
        const zoneSelected = selection?.kind === "zone" && selection.zoneId === zone.id;
        return (
          <div key={zone.id}>
            <TreeRow
              depth={0}
              label={zoneLabel(zone)}
              status={sdnZoneEntityStatus(zone.nodeStatus)}
              pending={zone.pending}
              selected={zoneSelected}
              onClick={() => {
                onSelect(zoneSel);
              }}
            />
            {zone.vnets.map((vnet) => {
              const vnetSel: SdnSelection = { kind: "vnet", zoneId: zone.id, vnetId: vnet.id };
              const vnetSelected =
                selection?.kind === "vnet" && selection.zoneId === zone.id && selection.vnetId === vnet.id;
              return (
                <div key={vnet.id}>
                  <TreeRow
                    depth={1}
                    label={vnetLabel(vnet)}
                    status="ok"
                    pending={vnet.pending}
                    selected={vnetSelected}
                    onClick={() => {
                      onSelect(vnetSel);
                    }}
                  />
                  {vnet.subnets.map((subnet) => {
                    const subnetSel: SdnSelection = {
                      kind: "subnet",
                      zoneId: zone.id,
                      vnetId: vnet.id,
                      subnetId: subnet.id,
                    };
                    const subnetSelected =
                      selection?.kind === "subnet" &&
                      selection.zoneId === zone.id &&
                      selection.vnetId === vnet.id &&
                      selection.subnetId === subnet.id;
                    return (
                      <TreeRow
                        key={subnet.id}
                        depth={2}
                        label={subnetLabel(subnet)}
                        status="ok"
                        pending={subnet.pending}
                        selected={subnetSelected}
                        onClick={() => {
                          onSelect(subnetSel);
                        }}
                      />
                    );
                  })}
                </div>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4 border-b border-slate-100 py-1 text-sm last:border-0 dark:border-slate-800">
      <dt className="text-slate-500 dark:text-slate-400">{label}</dt>
      <dd className="text-right text-slate-800 dark:text-slate-100">{value || "—"}</dd>
    </div>
  );
}

function ZoneDetail({ zone }: { zone: SdnZone }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{zone.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">Zone · {zone.type}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button variant="secondary" size="sm" onClick={() => { open({ kind: "vnet-create", zoneId: zone.id }); }}>
            + VNet
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "zone-edit", zone }); }}>
            Edit
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "zone-delete", zone }); }}>
            Delete
          </Button>
        </div>
      </div>
      {zone.pendingDiff ? <PendingDiffView diff={zone.pendingDiff} /> : <InSyncBadge />}
      <dl>
        <Field label="Bridge" value={zone.bridge ?? ""} />
        <Field label="Controller" value={zone.controller ?? ""} />
        <Field label="Nodes" value={(zone.nodes ?? []).join(", ")} />
        <Field label="Exit nodes" value={(zone.exitNodes ?? []).join(", ")} />
        <Field label="Peers" value={(zone.peers ?? []).join(", ")} />
        <Field label="MTU" value={zone.mtu ? String(zone.mtu) : ""} />
        <Field label="VRF VXLAN" value={zone.vrfVxlan ? String(zone.vrfVxlan) : ""} />
      </dl>
      <div>
        <h3 className="mb-2 text-sm font-medium text-slate-600 dark:text-slate-300">Per-node status</h3>
        <ul className="flex flex-col gap-1">
          {zone.nodeStatus.length === 0 && (
            <li className="text-sm text-slate-400">No per-node status reported yet.</li>
          )}
          {zone.nodeStatus.map((ns) => (
            <li key={ns.node} className="flex items-center gap-2 text-sm">
              <StatusDot status={sdnNodeEntityStatus(ns.status)} />
              <span className="font-medium">{ns.node}</span>
              <span className="text-slate-500 dark:text-slate-400">{ns.status || "ok"}</span>
              {ns.detail && <span className="text-xs text-slate-400">— {ns.detail}</span>}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

function VnetDetail({ vnet }: { vnet: SdnVnet }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{vnet.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">VNet · zone {vnet.zone}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button variant="secondary" size="sm" onClick={() => { open({ kind: "subnet-create", vnetId: vnet.id }); }}>
            + Subnet
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "vnet-edit", vnet }); }}>
            Edit
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "vnet-delete", vnet }); }}>
            Delete
          </Button>
        </div>
      </div>
      {vnet.pendingDiff ? <PendingDiffView diff={vnet.pendingDiff} /> : <InSyncBadge />}
      <dl>
        <Field label="Alias" value={vnet.alias ?? ""} />
        <Field label="Tag" value={vnet.tag ? String(vnet.tag) : ""} />
        <Field label="VLAN aware" value={vnet.vlanAware ? "yes" : "no"} />
      </dl>
    </div>
  );
}

function SubnetDetail({ subnet }: { subnet: SdnSubnet }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{subnet.cidr || subnet.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">Subnet · vnet {subnet.vnet}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "subnet-edit", subnet }); }}>
            Edit
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { open({ kind: "subnet-delete", subnet }); }}>
            Delete
          </Button>
        </div>
      </div>
      {subnet.pendingDiff ? <PendingDiffView diff={subnet.pendingDiff} /> : <InSyncBadge />}
      <dl>
        <Field label="Gateway" value={subnet.gateway ?? ""} />
        <Field label="DHCP range" value={subnet.dhcpRangeStart && subnet.dhcpRangeEnd ? `${subnet.dhcpRangeStart} – ${subnet.dhcpRangeEnd}` : ""} />
        <Field label="SNAT" value={subnet.snat ? "yes" : "no"} />
      </dl>
    </div>
  );
}

function TabButton({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={clsx(
        "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
        active
          ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
          : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800/60",
      )}
    >
      {label}
    </button>
  );
}

export function SdnPage() {
  const { data: tree, isLoading, isError } = useSdnQuery();
  const [selection, setSelection] = useState<SdnSelection | undefined>(undefined);
  const openEditor = useSdnEditorStore((s) => s.open);
  const [tab, setTab] = useState<SdnTab>("configuration");

  // Default-select the first zone once the tree first loads, so the detail
  // panel isn't blank on arrival (tree.ts's firstSelection).
  useEffect(() => {
    if (tree && !selection) {
      setSelection(firstSelection(tree));
    }
    // Only re-run when the tree reference changes; selection is
    // intentionally excluded so a user's manual click isn't overridden.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tree]);

  const resolved = resolveSdnSelection(tree, selection);

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">SDN</h1>
        <div className="flex items-center gap-2">
          <div role="tablist" aria-label="SDN cockpit views" className="flex items-center gap-1">
            <TabButton active={tab === "configuration"} label="Configuration" onClick={() => { setTab("configuration"); }} />
            <TabButton active={tab === "evpn"} label="EVPN / BGP" onClick={() => { setTab("evpn"); }} />
          </div>
          {tab === "configuration" && (
            <Button variant="primary" size="sm" onClick={() => { openEditor({ kind: "zone-create" }); }}>
              + New zone
            </Button>
          )}
        </div>
      </div>
      <SdnEditorLauncher />

      {tab === "configuration" && (
        <>
          {isLoading && <p className="text-sm text-slate-400">Loading SDN configuration…</p>}
          {isError && (
            <EmptyState
              title="Could not load SDN configuration"
              description="Check that vnproxd can reach the local PVE API, then reload."
            />
          )}
          {!isLoading && !isError && tree && tree.zones.length === 0 && (
            <EmptyState title="No SDN zones configured" description="Create a zone in Proxmox's SDN configuration to see it here." />
          )}

          {!isLoading && !isError && tree && tree.zones.length > 0 && (
            <div className="grid min-h-0 flex-1 grid-cols-[minmax(220px,320px)_1fr] gap-3">
              <div className="min-h-0 overflow-y-auto rounded-lg border border-slate-200 p-2 dark:border-slate-800">
                <SdnTreeView tree={tree} selection={selection} onSelect={setSelection} />
              </div>
              <div className="min-h-0 overflow-y-auto rounded-lg border border-slate-200 p-4 dark:border-slate-800">
                {!resolved && (
                  <EmptyState title="Nothing selected" description="Pick a zone, VNet, or subnet from the tree." />
                )}
                {resolved?.selection.kind === "zone" && <ZoneDetail zone={resolved.zone} />}
                {resolved?.selection.kind === "vnet" && resolved.vnet && <VnetDetail vnet={resolved.vnet} />}
                {resolved?.selection.kind === "subnet" && resolved.subnet && <SubnetDetail subnet={resolved.subnet} />}
              </div>
            </div>
          )}
        </>
      )}

      {tab === "evpn" && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <EvpnView />
        </div>
      )}
    </div>
  );
}
