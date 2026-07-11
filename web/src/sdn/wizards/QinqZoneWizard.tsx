// QinQ zone wizard (docs/features/sdn.md §2): "service VLAN + inner range
// with double-tag illustration."
//
// Flagged limitation (see previewEntities.ts/wizardOps.ts's own doc
// comments): this codebase's SdnZoneCreateParams has no zone-level
// service-VLAN field — T-401 and T-402's reports both already flagged
// QinQ's zone-level tag as unmodeled ("out of scope", "no QinQ-specific
// bridge semantics to distinguish"). The service tag entered here drives
// the preview's double-tag illustration and the wizard's own explanatory
// copy, but is not sent as an op param; only the customer tag (the
// VNet's own `tag`) is. See this task's completion report for the
// smallest-reasonable-extension call and the follow-up it flags.
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildQinqPreview, type QinqZoneParams } from "./previewEntities";
import { wizardStrings } from "./strings";
import { useClusterNodes } from "./useClusterNodes";
import { useWizardCapability } from "./useWizardCapability";
import { buildQinqZoneOps } from "./wizardOps";
import { WizardPreviewPane } from "./WizardPreviewPane";
import { WizardShell, type WizardStep } from "./WizardShell";
import { NodeCheckboxList } from "./NodeCheckboxList";

const S = wizardStrings;

export interface QinqZoneWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function QinqZoneWizard({ open, onOpenChange }: QinqZoneWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();
  const cap = useWizardCapability();

  const [zoneId, setZoneId] = useState("");
  const [bridgeName, setBridgeName] = useState("");
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [serviceVid, setServiceVid] = useState(0);
  const [customerVid, setCustomerVid] = useState(0);
  const [vnetId, setVnetId] = useState("");
  const [vnetAlias, setVnetAlias] = useState("");
  const [subnetCidr, setSubnetCidr] = useState("");
  const [subnetGateway, setSubnetGateway] = useState("");
  const [snat, setSnat] = useState(false);
  const [finishing, setFinishing] = useState(false);

  const params: QinqZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "qinq",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr,
      subnetGateway,
      snat,
      bridgeName,
      serviceVid,
      customerVid,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnetCidr, subnetGateway, snat, bridgeName, serviceVid, customerVid],
  );

  const graph = useMemo(() => buildQinqPreview(params), [params]);

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildQinqZoneOps(params);
    void addOps(ops, `QinQ network: ${zoneId} (customer VID ${String(customerVid)})`)
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
      isValid: zoneId.trim().length > 0 && bridgeName.trim().length > 0 && memberNodes.length > 0,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.qinq.intro}</p>
          <Field label="Name" help="e.g. tenant-net. Lowercase letters/digits, unique cluster-wide.">
            <input className={inputClass} value={zoneId} onChange={(e) => { setZoneId(e.target.value); }} placeholder="tenant-net" />
          </Field>
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
      id: "tags",
      title: "Double tag",
      isValid: customerVid > 0 && customerVid < 4095 && vnetId.trim().length > 0,
      content: (
        <div className="space-y-3">
          <h4 className="text-xs font-medium text-slate-600 dark:text-slate-300">{S.qinq.illustrationHeading}</h4>
          <p className="text-xs text-slate-500 dark:text-slate-400">{S.qinq.illustrationExplain}</p>
          <Field label="Service (outer) VLAN tag" help={S.qinq.serviceTagHelp}>
            <input
              type="number"
              className={inputClass}
              value={serviceVid || ""}
              onChange={(e) => { setServiceVid(Number(e.target.value)); }}
              min={1}
              max={4094}
            />
          </Field>
          <Field label="Customer (inner) VLAN tag" help={S.qinq.customerTagHelp}>
            <input
              type="number"
              className={inputClass}
              value={customerVid || ""}
              onChange={(e) => { setCustomerVid(Number(e.target.value)); }}
              min={1}
              max={4094}
            />
          </Field>
          <Field label="VNet name" help="e.g. vnet42. Unique cluster-wide.">
            <input className={inputClass} value={vnetId} onChange={(e) => { setVnetId(e.target.value); }} placeholder="vnet42" />
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
            <input className={inputClass} value={subnetCidr} onChange={(e) => { setSubnetCidr(e.target.value); }} placeholder="10.42.0.0/24" />
          </Field>
          {subnetCidr && (
            <>
              <Field label="Gateway" help={S.common.gatewayHelp}>
                <input className={inputClass} value={subnetGateway} onChange={(e) => { setSubnetGateway(e.target.value); }} placeholder="10.42.0.1" />
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
            <li>QinQ zone &quot;{zoneId}&quot; on bridge {bridgeName || "?"}, nodes {memberNodes.join(", ") || "none"}</li>
            <li>VNet &quot;{vnetId}&quot; (customer VID {customerVid})</li>
            {subnetCidr && <li>Subnet {subnetCidr}{snat ? " with SNAT" : ""}</li>}
          </ul>
          {serviceVid > 0 && (
            <p className="text-xs text-slate-400">
              Service tag {serviceVid} is shown for illustration only — set it directly in Proxmox&apos;s own SDN zone settings if not already configured (see this step&apos;s help text).
            </p>
          )}
        </div>
      ),
    },
  ];

  return (
    <WizardShell
      open={open}
      onOpenChange={onOpenChange}
      title={S.qinq.title}
      intro={S.qinq.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
