// Simple/SNAT zone wizard (docs/features/sdn.md §2): "isolated bridge per
// node, optional SNAT." The most minimal of the five — no VLAN/VXLAN
// concepts to explain, just naming + which nodes + an optional address
// range.
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildSimplePreview, type SimpleZoneParams } from "./previewEntities";
import { emptySubnetStepValue, SubnetStep, type SubnetStepValue } from "./SubnetStep";
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
  const [subnet, setSubnet] = useState<SubnetStepValue>(emptySubnetStepValue);
  const [finishing, setFinishing] = useState(false);

  const params: SimpleZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "simple",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr: subnet.cidr,
      subnetGateway: subnet.gateway,
      snat: subnet.snat,
      bridgeName,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnet, bridgeName],
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
      content: <SubnetStep zoneType="simple" value={subnet} onChange={setSubnet} />,
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
            {subnet.cidr && (
              <li>
                Subnet {subnet.cidr}
                {subnet.isolated ? " (isolated, no gateway)" : subnet.snat ? " with SNAT" : ""}
              </li>
            )}
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
