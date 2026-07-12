// SDN zone editor (docs/features/sdn.md §1/§4): type (create-only — changing
// a zone's type is a delete+create in real PVE too, params_sdn.go's doc
// comment), bridge, controller, member nodes, MTU, VRF VXLAN.
//
// This is the plain form editor, not the guided per-type wizard
// (docs/features/sdn.md §2's five wizards with live topology preview) —
// that's T-403's job; this editor is the "advanced"/direct-edit path T-402
// itself needs to satisfy "SDN editors UI (forms per entity)".
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { SdnZone } from "../../api/types";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import {
  buildSdnZoneCreateOp,
  buildSdnZoneUpdateOp,
  type SdnZoneFormValues,
} from "../../changesets/opBuilders";
import { useEditorSubmit } from "../../changesets/editors/useEditorSubmit";
import { EditorDialog, Field, inputClass } from "../../changesets/editors/EditorDialog";

export interface SdnZoneEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Existing zone — undefined for create. */
  existing?: SdnZone;
}

const ZONE_TYPES = [
  { value: "simple", label: "Simple — isolated bridge per node" },
  { value: "vlan", label: "VLAN — tagged on a shared trunk" },
  { value: "qinq", label: "QinQ — double-tagged VLAN" },
  { value: "vxlan", label: "VXLAN — tunneled overlay" },
  { value: "evpn", label: "EVPN — BGP-controlled VXLAN" },
];

function parseNodes(text: string): string[] {
  return text.split(",").map((s) => s.trim()).filter(Boolean);
}

export function SdnZoneEditor({ open, onOpenChange, existing }: SdnZoneEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: SdnZoneFormValues = {
    type: existing?.type ?? "simple",
    bridge: existing?.bridge ?? "",
    controller: existing?.controller ?? "",
    nodes: existing?.nodes ?? [],
    // exitNodes/peers (T-403) have no affordance in this plain form editor
    // — the guided EVPN/VXLAN wizards are where they're actually set; kept
    // at their existing value here so an edit through this form never
    // silently clears them.
    exitNodes: existing?.exitNodes ?? [],
    peers: existing?.peers ?? [],
    vrfVxlan: existing?.vrfVxlan ?? 0,
    mtu: existing?.mtu ?? 0,
  };

  const [type, setType] = useState(initial.type);
  const [bridge, setBridge] = useState(initial.bridge);
  const [controller, setController] = useState(initial.controller);
  const [nodesText, setNodesText] = useState(initial.nodes.join(", "));
  const [vrfVxlan, setVrfVxlan] = useState(initial.vrfVxlan);
  const [mtu, setMtu] = useState(initial.mtu);
  const [zoneId, setZoneId] = useState("");

  const isCreate = !existing;
  const target = existing ? `sdn-zone::${existing.id}` : `sdn-zone::${zoneId}`;
  // Cluster-scoped: no node segment, so capability resolves against the
  // "" cluster-wide entry (capabilities.ts's own documented fallback).
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");
  const usesBridge = type === "simple" || type === "vlan";
  const usesVxlanMtu = type === "vxlan" || type === "evpn";

  function handleSubmit(): void {
    if (isCreate && !zoneId.trim()) {
      toast({ title: "Zone name is required", variant: "error" });
      return;
    }
    const form: SdnZoneFormValues = {
      type,
      bridge,
      controller,
      nodes: parseNodes(nodesText),
      exitNodes: initial.exitNodes,
      peers: initial.peers,
      vrfVxlan,
      mtu,
    };
    const op = isCreate ? buildSdnZoneCreateOp(target, form) : buildSdnZoneUpdateOp(target, initial, form);
    const label = isCreate ? zoneId : existing.id;
    submit([op], `Edit sdn zone ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? "Create SDN zone" : `Edit SDN zone ${existing.id}`}
      description="A zone groups VNets that share one realization mechanism (bridge, VLAN trunk, or VXLAN/EVPN overlay) across the nodes you list."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Name" help="e.g. prodnet. Lowercase letters/digits, must be unique cluster-wide.">
          <input className={inputClass} value={zoneId} onChange={(e) => { setZoneId(e.target.value); }} placeholder="prodnet" />
        </Field>
      )}

      {isCreate && (
        <Field label="Type" help="What this zone actually does — see docs/features/sdn.md for the plain-English rundown of each.">
          <select className={inputClass} value={type} onChange={(e) => { setType(e.target.value); }}>
            {ZONE_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
      )}

      {usesBridge && (
        <Field label="Bridge" errors={findings.byField.bridge} help="The existing bridge (e.g. vmbr0) this zone realizes VNets on, on every member node.">
          <input className={inputClass} value={bridge} onChange={(e) => { setBridge(e.target.value); }} placeholder="vmbr0" />
        </Field>
      )}

      <Field label="Member nodes" errors={findings.byField.nodes} help="Comma-separated node names. Leave blank for every cluster node.">
        <input className={inputClass} value={nodesText} onChange={(e) => { setNodesText(e.target.value); }} placeholder="pve1, pve2, pve3" />
      </Field>

      {type === "evpn" && (
        <Field label="Controller" help="The EVPN controller instance this zone uses (configured separately under SDN → Controllers in PVE).">
          <input className={inputClass} value={controller} onChange={(e) => { setController(e.target.value); }} />
        </Field>
      )}

      {(type === "vxlan" || type === "evpn") && (
        <Field label="VRF VXLAN tag" help="Only needed for inter-VNet routing (EVPN).">
          <input type="number" className={inputClass} value={vrfVxlan} onChange={(e) => { setVrfVxlan(Number(e.target.value)); }} />
        </Field>
      )}

      <Field
        label="MTU"
        errors={findings.byField.mtu}
        help={usesVxlanMtu ? "Leave blank to use PVE's default (underlay MTU − 50, accounting for VXLAN encapsulation overhead)." : "Leave blank to use the bridge's own MTU."}
      >
        <input type="number" className={inputClass} value={mtu} onChange={(e) => { setMtu(Number(e.target.value)); }} />
      </Field>
    </EditorDialog>
  );
}
