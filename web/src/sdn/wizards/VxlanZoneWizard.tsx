// VXLAN zone wizard (docs/features/sdn.md §2): "peer address list
// auto-suggested from cluster node IPs; MTU math shown explicitly
// (underlay MTU − 50) with a one-click 'set VNet MTU accordingly'." —
// T-403 acceptance criterion 3 (MTU derivation shown visibly).
import { useEffect, useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildVxlanPreview, type VxlanZoneParams } from "./previewEntities";
import { wizardStrings } from "./strings";
import { useClusterNodes } from "./useClusterNodes";
import { useSuggestedPeers } from "./useSuggestedPeers";
import { useWizardCapability } from "./useWizardCapability";
import { computeVxlanMtuDerivation } from "./vxlanMath";
import { buildVxlanZoneOps } from "./wizardOps";
import { WizardPreviewPane } from "./WizardPreviewPane";
import { WizardShell, type WizardStep } from "./WizardShell";
import { NodeCheckboxList } from "./NodeCheckboxList";

const S = wizardStrings;

export interface VxlanZoneWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function VxlanZoneWizard({ open, onOpenChange }: VxlanZoneWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();
  const cap = useWizardCapability();

  const [zoneId, setZoneId] = useState("");
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [peers, setPeers] = useState<Record<string, string | undefined>>({});
  const [mtu, setMtu] = useState(0);
  const [vnetId, setVnetId] = useState("");
  const [vnetAlias, setVnetAlias] = useState("");
  const [vni, setVni] = useState(0);
  const [subnetCidr, setSubnetCidr] = useState("");
  const [subnetGateway, setSubnetGateway] = useState("");
  const [snat, setSnat] = useState(false);
  const [finishing, setFinishing] = useState(false);

  const suggested = useSuggestedPeers(memberNodes);

  // Seed (never overwrite) each newly-selected member node's peer field
  // from the auto-suggestion once it arrives — a user's own edit always
  // wins over a later-arriving suggestion.
  useEffect(() => {
    if (!suggested.ready) return;
    setPeers((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const node of memberNodes) {
        if (next[node] === undefined && suggested.byNode[node]) {
          next[node] = suggested.byNode[node];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [suggested.ready, suggested.byNode, memberNodes]);

  const derivation = computeVxlanMtuDerivation(mtu);

  const params: VxlanZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "vxlan",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr,
      subnetGateway,
      snat,
      mtu,
      vni,
      peers,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnetCidr, subnetGateway, snat, mtu, vni, peers],
  );

  const graph = useMemo(() => buildVxlanPreview(params), [params]);

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildVxlanZoneOps(params);
    void addOps(ops, `VXLAN network: ${zoneId}`)
      .then(() => {
        toast({ title: "Added to changeset", description: `${String(ops.length)} steps drafted — review before applying.` });
        onOpenChange(false);
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      })
      .finally(() => {
        setFinishing(false);
      });
  }

  const steps: WizardStep[] = [
    {
      id: "zone",
      title: "Peers",
      isValid: zoneId.trim().length > 0 && memberNodes.length > 0,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.vxlan.intro}</p>
          <Field label="Name" help="e.g. overlay1. Lowercase letters/digits, unique cluster-wide.">
            <input className={inputClass} value={zoneId} onChange={(e) => { setZoneId(e.target.value); }} placeholder="overlay1" />
          </Field>
          <NodeCheckboxList
            label="Member nodes"
            help={S.common.memberNodesHelp}
            allNodes={clusterNodes}
            selected={memberNodes}
            onChange={setMemberNodes}
          />
          {memberNodes.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-slate-500 dark:text-slate-400">{S.vxlan.peersStepHelp}</p>
              <p className="mb-1.5 text-[11px] text-slate-400">{S.vxlan.peersAutoSuggestNote}</p>
              <div className="space-y-1.5">
                {memberNodes.map((node) => (
                  <div key={node} className="flex items-center gap-2">
                    <span className="w-16 shrink-0 text-xs text-slate-500 dark:text-slate-400">{node}</span>
                    <input
                      className={inputClass}
                      value={peers[node] ?? ""}
                      onChange={(e) => { setPeers((p) => ({ ...p, [node]: e.target.value })); }}
                      placeholder={suggested.byNode[node] ?? "10.0.0.x"}
                    />
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ),
    },
    {
      id: "vnet-mtu",
      title: "VNet + MTU",
      isValid: vnetId.trim().length > 0,
      content: (
        <div className="space-y-3">
          <Field label="VNet name" help="e.g. vnet-overlay1. Unique cluster-wide.">
            <input className={inputClass} value={vnetId} onChange={(e) => { setVnetId(e.target.value); }} placeholder="vnet-overlay1" />
          </Field>
          <Field label="Alias" help={S.common.vnetAliasHelp}>
            <input className={inputClass} value={vnetAlias} onChange={(e) => { setVnetAlias(e.target.value); }} />
          </Field>
          <Field label="VNI" help="The tunnel identifier for this VNet — like a VLAN ID, but for the overlay.">
            <input type="number" className={inputClass} value={vni || ""} onChange={(e) => { setVni(Number(e.target.value)); }} />
          </Field>

          <div>
            <h4 className="mb-1 text-xs font-medium text-slate-600 dark:text-slate-300">{S.vxlan.mtuHeading}</h4>
            <p className="mb-1.5 text-xs text-slate-500 dark:text-slate-400">{S.vxlan.mtuExplain}</p>
            <Field label="Zone MTU (leave blank for PVE's default)">
              <input type="number" className={inputClass} value={mtu || ""} onChange={(e) => { setMtu(Number(e.target.value)); }} />
            </Field>
            <p className="mt-1 rounded bg-slate-100 p-1.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300" data-testid="vxlan-mtu-math">
              {S.vxlan.mtuMath(derivation.underlayMtu, derivation.overhead, derivation.safeMtu)}
            </p>
            {derivation.warn ? (
              <div className="mt-1.5 flex items-center justify-between gap-2 rounded border border-amber-300 bg-amber-50 p-1.5 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
                <span>{S.vxlan.mtuWarning(derivation.requestedMtu, derivation.safeMtu)}</span>
                <button
                  type="button"
                  className="shrink-0 rounded bg-amber-200 px-2 py-0.5 font-medium text-amber-900 hover:bg-amber-300 dark:bg-amber-800 dark:text-amber-100"
                  onClick={() => { setMtu(derivation.safeMtu); }}
                >
                  {S.vxlan.mtuApplyFix}
                </button>
              </div>
            ) : (
              mtu > 0 && (
                <p role="status" className="mt-1.5 text-xs text-emerald-600 dark:text-emerald-400">
                  {S.vxlan.mtuOk}
                </p>
              )
            )}
          </div>
        </div>
      ),
    },
    {
      id: "subnet",
      title: "Addresses",
      isValid: true,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.common.subnetSkipHelp}</p>
          <Field label="Address range (CIDR)" help={S.common.cidrHelp}>
            <input className={inputClass} value={subnetCidr} onChange={(e) => { setSubnetCidr(e.target.value); }} placeholder="10.90.0.0/24" />
          </Field>
          {subnetCidr && (
            <>
              <Field label="Gateway" help={S.common.gatewayHelp}>
                <input className={inputClass} value={subnetGateway} onChange={(e) => { setSubnetGateway(e.target.value); }} placeholder="10.90.0.1" />
              </Field>
              <Field label="Internet access (SNAT)" help={S.common.snatHelp}>
                <label className="flex items-center gap-2">
                  <input type="checkbox" checked={snat} onChange={(e) => { setSnat(e.target.checked); }} />
                  Enable SNAT
                </label>
              </Field>
            </>
          )}
        </div>
      ),
    },
    {
      id: "review",
      title: "Review",
      isValid: !cap.denied,
      invalidReason: cap.reason,
      content: (
        <div className="space-y-2 text-slate-600 dark:text-slate-300">
          <p>This will draft:</p>
          <ul className="list-inside list-disc space-y-1">
            <li>VXLAN zone &quot;{zoneId}&quot; on {memberNodes.join(", ") || "no nodes"}, MTU {mtu || derivation.safeMtu}</li>
            <li>VNet &quot;{vnetId}&quot;{vni ? ` (VNI ${String(vni)})` : ""}</li>
            {subnetCidr && <li>Subnet {subnetCidr}{snat ? " with SNAT" : ""}</li>}
          </ul>
        </div>
      ),
    },
  ];

  return (
    <WizardShell
      open={open}
      onOpenChange={onOpenChange}
      title={S.vxlan.title}
      intro={S.vxlan.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
