// SDN cockpit: zone -> vnet -> subnet tree with per-node realization status
// plus a detail panel rendering the staged-vs-running pending diff
// (docs/features/sdn.md §1). Read-only for T-401 — T-402 added the plain
// form editors (SdnZoneEditor et al.); T-403 added the guided per-type
// wizards with live preview (wizards/ZoneWizardPicker).
import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import type { SdnSubnet, SdnTree, SdnVnet, SdnZone } from "../api/types";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { SdnEditorLauncher } from "./SdnEditorLauncher";
import { DhcpView } from "./DhcpView";
import { EvpnView } from "./EvpnView";
import { FabricsView } from "./FabricsView";
import { InSyncBadge, PendingDiffView } from "./PendingDiffView";
import { useSdnEditorStore } from "./sdnEditorStore";
import { StatusDot } from "./StatusDot";
import { sdnNodeEntityStatus, sdnZoneEntityStatus } from "./status";
import { firstSelection, resolveSdnSelection, type SdnSelection } from "./tree";
import { useSdnQuery } from "./queries";
import { ZoneWizardPicker, type WizardKind } from "./wizards/ZoneWizardPicker";

type SdnTab = "configuration" | "evpn" | "dhcp" | "fabrics";

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
      // T-905 (axe aria-required-children): the enclosing role="tree" needs
      // role="treeitem" children — each SDN zone/vnet/subnet row is one.
      // `aria-selected` and `aria-level` (depth+1) give the treeitem its
      // required state and position; the nested vnet/subnet lists are wrapped
      // in role="group" by the tree renderer below.
      role="treeitem"
      aria-selected={selected}
      aria-level={depth + 1}
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

/** SDN's write privilege (`SDN.Allocate`) is cluster-wide in PVE, not
 * per-node — resolved via hasAnyCap (T-607 fix: internal/auth.
 * BuildCapabilities only ever populates a `""` caps-map key in the
 * zero-nodes edge case; a real multi-node session's caps map has only
 * per-node keys, so the `capsForNode(session, "")` idiom this file
 * originally used — copied from five pre-existing SDN editor dialogs that
 * had the same bug, see the same-day fix to those — silently evaluates to
 * NO_CAPS and disables every cluster-scope SDN write control permanently
 * in any real multi-node cluster, root included; caught for real by this
 * task's own new E2E wizard-completion test, not by any unit test, since
 * every existing wizard/editor unit test's mock session fixture happens to
 * include an artificial `""` key that masked it). Threaded down as a
 * single prop so every zone/vnet/subnet edit/delete/create trigger
 * disables (with the standard missing-privilege tooltip) the same way the
 * topology inspector's edit/delete buttons already do (docs/user-guide.md
 * §5, T-605 AC4's read-only-sweep contract) — these trigger buttons
 * previously had no gating at all (only the modal editors/delete-dialogs
 * they open did), a real gap this task's E2E read-only crawl caught. */
interface SdnWriteGate {
  disabled: boolean;
  title: string | undefined;
}

function useSdnWriteGate(): SdnWriteGate {
  const { data: session } = useSession();
  const canWrite = hasAnyCap(session, "sdnWrite");
  return {
    disabled: !canWrite,
    title: canWrite ? undefined : missingCapTooltip(session, "", "sdnWrite"),
  };
}

function ZoneDetail({ zone, gate }: { zone: SdnZone; gate: SdnWriteGate }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{zone.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">Zone · {zone.type}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button
            variant="secondary"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "vnet-create", zoneId: zone.id }); }}
          >
            + VNet
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "zone-edit", zone }); }}
          >
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "zone-delete", zone }); }}
          >
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

function VnetDetail({ vnet, gate }: { vnet: SdnVnet; gate: SdnWriteGate }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{vnet.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">VNet · zone {vnet.zone}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button
            variant="secondary"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "subnet-create", vnetId: vnet.id }); }}
          >
            + Subnet
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "vnet-edit", vnet }); }}
          >
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "vnet-delete", vnet }); }}
          >
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

function SubnetDetail({ subnet, gate }: { subnet: SdnSubnet; gate: SdnWriteGate }) {
  const open = useSdnEditorStore((s) => s.open);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">{subnet.cidr || subnet.id}</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">Subnet · vnet {subnet.vnet}</p>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "subnet-edit", subnet }); }}
          >
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={gate.disabled}
            title={gate.title}
            onClick={() => { open({ kind: "subnet-delete", subnet }); }}
          >
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
  const [wizardPickerOpen, setWizardPickerOpen] = useState(false);
  const [wizardInitialKind, setWizardInitialKind] = useState<WizardKind | undefined>(undefined);
  const gate = useSdnWriteGate();

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

  // T-903 command-palette verb: "New VLAN zone" jumps straight past the
  // zone-type picker grid into the VLAN wizard (ZoneWizardPicker's
  // `initialActive`); gated on the same write capability the toolbar's own
  // "+ New zone (guided)" button already checks, so the palette never
  // offers a write a read-only session can't perform.
  const sdnPaletteActions = useMemo<PaletteAction[]>(
    () =>
      gate.disabled
        ? []
        : [
            {
              id: "sdn-new-vlan-zone",
              label: "New VLAN zone",
              hint: "SDN",
              perform: () => {
                setWizardInitialKind("vlan");
                setWizardPickerOpen(true);
              },
            },
          ],
    [gate.disabled],
  );
  usePaletteActions("sdn", sdnPaletteActions);

  const resolved = resolveSdnSelection(tree, selection);

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">SDN</h1>
        <div className="flex items-center gap-2">
          <div role="tablist" aria-label="SDN cockpit views" className="flex items-center gap-1">
            <TabButton active={tab === "configuration"} label="Configuration" onClick={() => { setTab("configuration"); }} />
            <TabButton active={tab === "evpn"} label="EVPN / BGP" onClick={() => { setTab("evpn"); }} />
            <TabButton active={tab === "dhcp"} label="DHCP" onClick={() => { setTab("dhcp"); }} />
            <TabButton active={tab === "fabrics"} label="Fabrics" onClick={() => { setTab("fabrics"); }} />
          </div>
          {tab === "configuration" && (
            <>
              <Button
                variant="primary"
                size="sm"
                disabled={gate.disabled}
                title={gate.title}
                onClick={() => {
                  setWizardInitialKind(undefined);
                  setWizardPickerOpen(true);
                }}
              >
                + New zone (guided)
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={gate.disabled}
                title={gate.title}
                onClick={() => { openEditor({ kind: "zone-create" }); }}
              >
                + New zone (advanced)
              </Button>
            </>
          )}
        </div>
      </div>
      <SdnEditorLauncher />
      <ZoneWizardPicker open={wizardPickerOpen} onOpenChange={setWizardPickerOpen} initialActive={wizardInitialKind} />

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
                {resolved?.selection.kind === "zone" && <ZoneDetail zone={resolved.zone} gate={gate} />}
                {resolved?.selection.kind === "vnet" && resolved.vnet && <VnetDetail vnet={resolved.vnet} gate={gate} />}
                {resolved?.selection.kind === "subnet" && resolved.subnet && <SubnetDetail subnet={resolved.subnet} gate={gate} />}
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

      {tab === "dhcp" && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <DhcpView />
        </div>
      )}

      {tab === "fabrics" && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <FabricsView />
        </div>
      )}
    </div>
  );
}
