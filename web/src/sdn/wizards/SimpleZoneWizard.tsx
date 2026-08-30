// SPDX-License-Identifier: Apache-2.0

// Simple/SNAT zone wizard (docs/features/sdn.md §2): "isolated bridge per
// node, optional SNAT." The most minimal of the five — no VLAN/VXLAN
// concepts to explain, just naming + which nodes + an optional address
// range.
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildSimplePreview, type SimpleZoneParams } from "./previewEntities";
import { SubnetStep, type SubnetStepValue } from "./SubnetStep";
import { DEFAULT_VNET_ID, DEFAULT_ZONE_ID, defaultSubnetStepValue, useSelectAllNodesOnce } from "./wizardDefaults";
import { SdnNameField } from "./SdnNameField";
import { sdnNameError, subnetStepValid } from "./validation";
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

  // Seeded with click-through defaults (see wizardDefaults.ts): a user can
  // accept every step and deploy a working SDN network without typing.
  const [zoneId, setZoneId] = useState(DEFAULT_ZONE_ID);
  const [bridgeName, setBridgeName] = useState("");
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [vnetId, setVnetId] = useState(DEFAULT_VNET_ID);
  const [vnetAlias, setVnetAlias] = useState("");
  const [subnet, setSubnet] = useState<SubnetStepValue>(defaultSubnetStepValue);
  const [finishing, setFinishing] = useState(false);

  // Default to every cluster node selected (the sensible default for a
  // cluster-wide simple zone), once, without fighting a manual change.
  useSelectAllNodesOnce(clusterNodes, memberNodes, setMemberNodes);

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
      isValid: zoneId.trim().length > 0 && memberNodes.length > 0 && !sdnNameError(zoneId),
      content: (
        <div className="space-y-3">
          <p className="text-fg-muted">{S.simple.intro}</p>
          <SdnNameField label="Name" help={S.common.zoneNameHelp} value={zoneId} onChange={setZoneId} placeholder="homelab" />
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
      isValid: vnetId.trim().length > 0 && !sdnNameError(vnetId),
      content: (
        <div className="space-y-3">
          <p className="text-fg-muted">
            VMs plug into a VNet, not the zone directly — the zone is the mechanism, the VNet is what you actually attach a VM's network card to.
          </p>
          <SdnNameField label="Name" help={S.common.vnetNameHelp} value={vnetId} onChange={setVnetId} placeholder="vnet1" />
          <Field label="Alias" help={S.common.vnetAliasHelp}>
            <input className={inputClass} value={vnetAlias} onChange={(e) => { setVnetAlias(e.target.value); }} />
          </Field>
        </div>
      ),
    },
    {
      id: "subnet",
      title: "Addresses",
      isValid: subnetStepValid(subnet),
      content: <SubnetStep zoneType="simple" value={subnet} onChange={setSubnet} />,
    },
    {
      id: "review",
      title: "Review",
      isValid: !cap.denied,
      invalidReason: cap.reason,
      content: (
        <div className="space-y-2 text-fg-muted">
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
      helpTopic="sdn-zone-wizard"
      intro={S.simple.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
