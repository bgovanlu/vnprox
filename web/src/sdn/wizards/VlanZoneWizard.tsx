// SPDX-License-Identifier: Apache-2.0

// VLAN zone wizard (docs/features/sdn.md §2): "picks the VLAN-aware
// bridge, validates the physical path actually trunks the chosen VIDs
// (cross-checks LLDP VLAN info when available)." — T-403 acceptance
// criterion 2.
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildVlanPreview, type VlanZoneParams } from "./previewEntities";
import { SubnetStep, type SubnetStepValue } from "./SubnetStep";
import { DEFAULT_BRIDGE, DEFAULT_VLAN_TAG, DEFAULT_VNET_ID, DEFAULT_ZONE_ID, defaultSubnetStepValue, useSelectAllNodesOnce } from "./wizardDefaults";
import { SdnNameField } from "./SdnNameField";
import { sdnNameError, subnetStepValid } from "./validation";
import { wizardStrings } from "./strings";
import { useClusterNodes } from "./useClusterNodes";
import { useLldpTrunkCheck } from "./useLldpTrunkCheck";
import { useWizardCapability } from "./useWizardCapability";
import { buildVlanZoneOps } from "./wizardOps";
import { WizardPreviewPane } from "./WizardPreviewPane";
import { WizardShell, type WizardStep } from "./WizardShell";
import { NodeCheckboxList } from "./NodeCheckboxList";

const S = wizardStrings;

export interface VlanZoneWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function VlanZoneWizard({ open, onOpenChange }: VlanZoneWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();
  const cap = useWizardCapability();

  // Seeded with click-through defaults (see wizardDefaults.ts).
  const [zoneId, setZoneId] = useState(DEFAULT_ZONE_ID);
  const [bridgeName, setBridgeName] = useState(DEFAULT_BRIDGE);
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [vid, setVid] = useState(DEFAULT_VLAN_TAG);
  const [vnetId, setVnetId] = useState(DEFAULT_VNET_ID);
  const [vnetAlias, setVnetAlias] = useState("");
  const [subnet, setSubnet] = useState<SubnetStepValue>(defaultSubnetStepValue);
  useSelectAllNodesOnce(clusterNodes, memberNodes, setMemberNodes);
  const [finishing, setFinishing] = useState(false);

  const trunkCheck = useLldpTrunkCheck(bridgeName, memberNodes, vid);

  const params: VlanZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "vlan",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr: subnet.cidr,
      subnetGateway: subnet.gateway,
      snat: subnet.snat,
      bridgeName,
      vid,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnet, bridgeName, vid],
  );

  const graph = useMemo(() => buildVlanPreview(params), [params]);

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildVlanZoneOps(params);
    void addOps(ops, `VLAN network: ${zoneId} (VID ${String(vid)})`)
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
      title: "Trunk",
      isValid: zoneId.trim().length > 0 && bridgeName.trim().length > 0 && memberNodes.length > 0 && !sdnNameError(zoneId),
      content: (
        <div className="space-y-3">
          <p className="text-fg-muted">{S.vlan.intro}</p>
          <p className="text-fg-muted">{S.vlan.bridgeStepHelp}</p>
          <SdnNameField label="Name" help={S.common.zoneNameHelp} value={zoneId} onChange={setZoneId} placeholder="prodnet" />
          <Field label="VLAN-aware bridge" help="Must exist, with this exact name, on every member node.">
            <input className={inputClass} value={bridgeName} onChange={(e) => { setBridgeName(e.target.value); }} placeholder="vmbr0" />
          </Field>
          <NodeCheckboxList
            label="Member nodes"
            help={S.common.memberNodesHelp}
            allNodes={clusterNodes}
            selected={memberNodes}
            onChange={setMemberNodes}
          />
        </div>
      ),
    },
    {
      id: "vid",
      title: "VLAN ID + trunk check",
      isValid: vid > 0 && vid < 4095 && vnetId.trim().length > 0 && !sdnNameError(vnetId),
      content: (
        <div className="space-y-3">
          <Field label="VLAN ID (VID)" help={S.vlan.vidHelp}>
            <input
              type="number"
              className={inputClass}
              value={vid || ""}
              onChange={(e) => { setVid(Number(e.target.value)); }}
              min={1}
              max={4094}
            />
          </Field>
          <SdnNameField label="VNet name" help={S.common.vnetNameHelp} value={vnetId} onChange={setVnetId} placeholder="vnet300" />
          <Field label="Alias" help={S.common.vnetAliasHelp}>
            <input className={inputClass} value={vnetAlias} onChange={(e) => { setVnetAlias(e.target.value); }} />
          </Field>

          <div>
            <h4 className="mb-1 text-xs font-medium text-fg-muted">{S.vlan.trunkCheckHeading}</h4>
            <p className="mb-1.5 text-xs text-fg-subtle">{S.vlan.trunkCheckExplain}</p>
            {vid <= 0 && <p className="text-xs text-fg-muted">Enter a VLAN ID above to run the check.</p>}
            {vid > 0 && !trunkCheck.ready && <p className="text-xs text-fg-muted">{S.common.previewLoading}</p>}
            {vid > 0 && trunkCheck.ready && !trunkCheck.hasData && (
              <p className="text-xs text-fg-subtle">{S.vlan.trunkCheckNoData}</p>
            )}
            {vid > 0 && trunkCheck.ready && trunkCheck.hasData && trunkCheck.warnings.length === 0 && (
              <p role="status" className="text-xs text-emerald-600 dark:text-emerald-400">
                {S.vlan.trunkCheckOk}
              </p>
            )}
            {vid > 0 && trunkCheck.ready && trunkCheck.hasData && trunkCheck.warnings.length > 0 && (
              <ul className="space-y-1" role="alert">
                {trunkCheck.warnings.map((w) => (
                  <li
                    key={w.neighborRef}
                    className="rounded border border-amber-300 bg-amber-50 p-1.5 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200"
                  >
                    {S.vlan.trunkCheckWarning(w.portId, w.chassisName, vid)}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      ),
    },
    {
      id: "subnet",
      title: "Addresses",
      isValid: subnetStepValid(subnet),
      content: <SubnetStep zoneType="vlan" value={subnet} onChange={setSubnet} />,
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
            <li>VLAN zone &quot;{zoneId}&quot; on bridge {bridgeName || "?"}, nodes {memberNodes.join(", ") || "none"}</li>
            <li>VNet &quot;{vnetId}&quot; (VID {vid})</li>
            {subnet.cidr && (
              <li>
                Subnet {subnet.cidr}
                {subnet.isolated ? " (isolated, no gateway)" : subnet.snat ? " with SNAT" : ""}
              </li>
            )}
            {trunkCheck.hasData && trunkCheck.warnings.length > 0 && (
              <li className="text-amber-700 dark:text-amber-300">{trunkCheck.warnings.length} trunk-check warning(s) — see the previous step.</li>
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
      title={S.vlan.title}
      helpTopic="sdn-zone-wizard"
      intro={S.vlan.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
