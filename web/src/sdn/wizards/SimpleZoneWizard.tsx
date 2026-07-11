// Simple/SNAT zone wizard (docs/features/sdn.md §2): "isolated bridge per
// node, optional SNAT." The most minimal of the five — no VLAN/VXLAN
// concepts to explain, just naming + which nodes + an optional address
// range.
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildSimplePreview, type SimpleZoneParams } from "./previewEntities";
import { wizardStrings } from "./strings";
import { useClusterNodes } from "./useClusterNodes";
import { useWizardCapability } from "./useWizardCapability";
import { buildSimpleZoneOps } from "./wizardOps";
import { WizardPreviewPane } from "./WizardPreviewPane";
import { WizardShell, type WizardStep } from "./WizardShell";
import { NodeCheckboxList } from "./NodeCheckboxList";

const S = wizardStrings;

export interface SimpleZoneWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function SimpleZoneWizard({ open, onOpenChange }: SimpleZoneWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();
  const cap = useWizardCapability();

  const [zoneId, setZoneId] = useState("");
  const [bridgeName, setBridgeName] = useState("");
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [vnetId, setVnetId] = useState("");
  const [vnetAlias, setVnetAlias] = useState("");
  const [subnetCidr, setSubnetCidr] = useState("");
  const [subnetGateway, setSubnetGateway] = useState("");
  const [snat, setSnat] = useState(false);
  const [finishing, setFinishing] = useState(false);

  const params: SimpleZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "simple",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr,
      subnetGateway,
      snat,
      bridgeName,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnetCidr, subnetGateway, snat, bridgeName],
  );

  const graph = useMemo(() => buildSimplePreview(params), [params]);

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildSimpleZoneOps(params);
    void addOps(ops, `Simple network: ${zoneId}`)
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
      title: "Network",
      isValid: zoneId.trim().length > 0 && memberNodes.length > 0,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.simple.intro}</p>
          <Field label="Name" help="e.g. homelab. Lowercase letters/digits, unique cluster-wide.">
            <input className={inputClass} value={zoneId} onChange={(e) => { setZoneId(e.target.value); }} placeholder="homelab" />
          </Field>
          <NodeCheckboxList
            label="Member nodes"
            help={S.common.memberNodesHelp}
            allNodes={clusterNodes}
            selected={memberNodes}
            onChange={setMemberNodes}
          />
          <Field label="Bridge name (optional)" help={S.simple.bridgeNameHelp}>
            <input className={inputClass} value={bridgeName} onChange={(e) => { setBridgeName(e.target.value); }} placeholder="auto" />
          </Field>
        </div>
      ),
    },
    {
      id: "vnet",
      title: "VMs attach here",
      isValid: vnetId.trim().length > 0,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">
            VMs plug into a VNet, not the zone directly — the zone is the mechanism, the VNet is what you actually attach a VM's network card to.
          </p>
          <Field label="Name" help="e.g. vnet1. Unique cluster-wide.">
            <input className={inputClass} value={vnetId} onChange={(e) => { setVnetId(e.target.value); }} placeholder="vnet1" />
          </Field>
          <Field label="Alias" help={S.common.vnetAliasHelp}>
            <input className={inputClass} value={vnetAlias} onChange={(e) => { setVnetAlias(e.target.value); }} />
          </Field>
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
            <input className={inputClass} value={subnetCidr} onChange={(e) => { setSubnetCidr(e.target.value); }} placeholder="10.50.0.0/24" />
          </Field>
          {subnetCidr && (
            <>
              <Field label="Gateway" help={S.common.gatewayHelp}>
                <input className={inputClass} value={subnetGateway} onChange={(e) => { setSubnetGateway(e.target.value); }} placeholder="10.50.0.1" />
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
            <li>Zone &quot;{zoneId}&quot; on {memberNodes.join(", ") || "no nodes selected"}</li>
            <li>VNet &quot;{vnetId}&quot;{vnetAlias ? ` (${vnetAlias})` : ""}</li>
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
      title={S.simple.title}
      intro={S.simple.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
