// SPDX-License-Identifier: Apache-2.0

// EVPN zone wizard (docs/features/sdn.md §2): "controller (ASN, peers),
// VRF-VXLAN tag, exit nodes, primary exit, route-target explanation. The
// wizard renders the resulting BGP session graph before creation." —
// T-403 acceptance criterion 3 (3-peer session graph).
//
// Flagged limitation: this codebase's op vocabulary has no
// `sdn.controller.create` (T-603's Blueprints report flags this exact gap
// and calls it out as "squarely T-403's territory" — see this task's
// completion report for why a full controller CRUD op family was judged
// out of proportion for a "zone wizards" card and left as a precisely-
// scoped follow-up instead of built here). The controller field below is
// a reference to an existing PVE-configured controller, exactly like
// SdnZoneEditor.tsx's own Controller field; ASN/peer addresses are
// preview-only (they draw the BGP session graph) and are not sent as op
// params beyond the zone's own `peers`/`exitNodes` fields (T-403's write-
// path fix — see params_sdn.go).
import { useMemo, useState } from "react";
import { useToast } from "../../components/Toast";
import { useDrawerActions } from "../../changesets/useDrawerActions";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { buildEvpnPreview, type EvpnZoneParams } from "./previewEntities";
import { SubnetStep, type SubnetStepValue } from "./SubnetStep";
import { DEFAULT_EVPN_ASN, DEFAULT_EVPN_CONTROLLER, DEFAULT_VNET_ID, DEFAULT_VNI, DEFAULT_ZONE_ID, defaultSubnetStepValue, useSelectAllNodesOnce } from "./wizardDefaults";
import { SdnNameField } from "./SdnNameField";
import { isValidIp, sdnNameError, subnetStepValid, vniError } from "./validation";
import { wizardStrings } from "./strings";
import { useClusterNodes } from "./useClusterNodes";
import { useWizardCapability } from "./useWizardCapability";
import { buildEvpnZoneOps } from "./wizardOps";
import { WizardPreviewPane } from "./WizardPreviewPane";
import { WizardShell, type WizardStep } from "./WizardShell";
import { NodeCheckboxList } from "./NodeCheckboxList";

const S = wizardStrings;

export interface EvpnZoneWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function parseAddressList(text: string): string[] {
  return text
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function EvpnZoneWizard({ open, onOpenChange }: EvpnZoneWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const clusterNodes = useClusterNodes();
  const cap = useWizardCapability();

  // Seeded with click-through defaults (see wizardDefaults.ts). Peers and
  // exit nodes stay optional/empty — they're genuinely deployment-specific
  // and the zone validates without them.
  const [zoneId, setZoneId] = useState(DEFAULT_ZONE_ID);
  const [memberNodes, setMemberNodes] = useState<string[]>([]);
  const [controller, setController] = useState(DEFAULT_EVPN_CONTROLLER);
  const [asn, setAsn] = useState(DEFAULT_EVPN_ASN);
  const [peerAddressesText, setPeerAddressesText] = useState("");
  const [vrfVxlan, setVrfVxlan] = useState(0);
  const [exitNodes, setExitNodes] = useState<string[]>([]);
  const [vnetId, setVnetId] = useState(DEFAULT_VNET_ID);
  const [vnetAlias, setVnetAlias] = useState("");
  const [vni, setVni] = useState(DEFAULT_VNI);
  const [subnet, setSubnet] = useState<SubnetStepValue>(defaultSubnetStepValue);
  useSelectAllNodesOnce(clusterNodes, memberNodes, setMemberNodes);
  const [finishing, setFinishing] = useState(false);

  const peerAddresses = useMemo(() => parseAddressList(peerAddressesText), [peerAddressesText]);
  // Every entered peer must be a valid IP — a typo'd underlay address is a
  // classic mid-apply PVE rejection (issue #3). An empty list is allowed
  // (peers are optional in the wizard; the zone still validates).
  const peerListError = peerAddresses.some((a) => !isValidIp(a)) ? "One or more peer addresses aren't valid IP addresses." : undefined;
  const vniErr = vniError(vni);

  const params: EvpnZoneParams = useMemo(
    () => ({
      zoneId,
      zoneType: "evpn",
      memberNodes,
      vnetId,
      vnetAlias,
      subnetCidr: subnet.cidr,
      subnetGateway: subnet.gateway,
      snat: subnet.snat,
      controller,
      asn,
      peerAddresses,
      exitNodes,
      vrfVxlan,
      vni,
    }),
    [zoneId, memberNodes, vnetId, vnetAlias, subnet, controller, asn, peerAddresses, exitNodes, vrfVxlan, vni],
  );

  const graph = useMemo(() => buildEvpnPreview(params), [params]);

  function toggleExitNode(node: string): void {
    setExitNodes((prev) => (prev.includes(node) ? prev.filter((n) => n !== node) : [...prev, node]));
  }

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildEvpnZoneOps(params);
    void addOps(ops, `EVPN network: ${zoneId}`)
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
      id: "controller",
      title: "Controller",
      isValid: zoneId.trim().length > 0 && memberNodes.length > 0 && !sdnNameError(zoneId) && !peerListError,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.evpn.intro}</p>
          <SdnNameField label="Name" help={S.common.zoneNameHelp} value={zoneId} onChange={setZoneId} placeholder="dcevpn" />
          <NodeCheckboxList
            label="Member nodes"
            help={S.common.memberNodesHelp}
            allNodes={clusterNodes}
            selected={memberNodes}
            onChange={setMemberNodes}
          />
          <Field label="Controller ID" help={S.evpn.controllerStepHelp}>
            <input className={inputClass} value={controller} onChange={(e) => { setController(e.target.value); }} placeholder="evpn1" />
          </Field>
          <Field label="ASN (preview only)" help={S.evpn.asnHelp}>
            <input type="number" className={inputClass} value={asn || ""} onChange={(e) => { setAsn(Number(e.target.value)); }} placeholder="65000" />
          </Field>
          <Field
            label="Peer addresses"
            help="Comma-separated underlay IPs — draws the BGP session graph below."
            errors={peerListError ? [peerListError] : undefined}
          >
            <input
              className={inputClass}
              value={peerAddressesText}
              onChange={(e) => { setPeerAddressesText(e.target.value); }}
              placeholder="10.10.0.11, 10.10.0.12, 10.10.0.13"
            />
          </Field>
        </div>
      ),
    },
    {
      id: "exit-nodes",
      title: "Exit nodes",
      isValid: true,
      content: (
        <div className="space-y-3">
          <p className="text-slate-600 dark:text-slate-300">{S.evpn.exitNodesHelp}</p>
          <div className="flex flex-wrap gap-3">
            {memberNodes.length === 0 && <p className="text-xs text-slate-600 dark:text-slate-400">Pick member nodes on the previous step first.</p>}
            {memberNodes.map((node) => (
              <label key={node} className="flex items-center gap-1.5 text-sm">
                <input type="checkbox" checked={exitNodes.includes(node)} onChange={() => { toggleExitNode(node); }} />
                {node}
              </label>
            ))}
          </div>
          {exitNodes.length > 0 && <p className="text-xs text-slate-600 dark:text-slate-400">{S.evpn.primaryExitHelp} ({exitNodes[0]})</p>}
          <Field label="VRF VXLAN tag (optional)" help="Only needed for inter-VNet routing.">
            <input type="number" className={inputClass} value={vrfVxlan || ""} onChange={(e) => { setVrfVxlan(Number(e.target.value)); }} />
          </Field>
          <p className="text-xs text-slate-600 dark:text-slate-400">{S.evpn.routeTargetExplain}</p>
        </div>
      ),
    },
    {
      id: "vnet",
      title: "VMs attach here",
      isValid: vnetId.trim().length > 0 && !sdnNameError(vnetId) && !vniError(vni) && subnetStepValid(subnet),
      content: (
        <div className="space-y-3">
          <SdnNameField label="VNet name" help={S.common.vnetNameHelp} value={vnetId} onChange={setVnetId} placeholder="vnetdc1" />
          <Field label="Alias" help={S.common.vnetAliasHelp}>
            <input className={inputClass} value={vnetAlias} onChange={(e) => { setVnetAlias(e.target.value); }} />
          </Field>
          <Field label="VNI" help={S.vxlan.vniHelp} errors={vniErr ? [vniErr] : undefined}>
            <input type="number" className={inputClass} value={vni || ""} onChange={(e) => { setVni(Number(e.target.value)); }} min={1} max={4094} />
          </Field>
          <SubnetStep zoneType="evpn" value={subnet} onChange={setSubnet} evpnExitNodeCount={exitNodes.length} />
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
          <div>
            <h4 className="mb-1 text-xs font-medium text-slate-600 dark:text-slate-300">{S.evpn.sessionGraphHeading}</h4>
            <p className="text-xs text-slate-500 dark:text-slate-400">{S.evpn.sessionGraphExplain}</p>
          </div>
          <p>This will draft:</p>
          <ul className="list-inside list-disc space-y-1">
            <li>EVPN zone &quot;{zoneId}&quot; on {memberNodes.join(", ") || "no nodes"}</li>
            {controller && <li>Referencing controller &quot;{controller}&quot;</li>}
            {exitNodes.length > 0 && <li>Exit nodes: {exitNodes.join(", ")} (primary: {exitNodes[0]})</li>}
            {peerAddresses.length > 0 && <li>Peers: {peerAddresses.join(", ")}</li>}
            <li>VNet &quot;{vnetId}&quot;{vni ? ` (VNI ${String(vni)})` : ""}</li>
            {subnet.cidr && (
              <li>
                Subnet {subnet.cidr}
                {subnet.isolated ? " (no gateway)" : subnet.snat ? " with SNAT" : ""}
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
      title={S.evpn.title}
      helpTopic="sdn-zone-wizard"
      intro={S.evpn.intro}
      steps={steps}
      preview={<WizardPreviewPane graph={graph} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}
