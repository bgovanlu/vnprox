// SDN VNet editor: zone (create-only), alias, tag (VLAN ID / VNI), VLAN-aware.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { SdnVnet } from "../../api/types";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import {
  buildSdnVnetCreateOp,
  buildSdnVnetUpdateOp,
  type SdnVnetFormValues,
} from "../../changesets/opBuilders";
import { useEditorSubmit } from "../../changesets/editors/useEditorSubmit";
import { EditorDialog, Field, inputClass } from "../../changesets/editors/EditorDialog";

export interface SdnVnetEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Owning zone id — create-time only (an existing vnet's zone is fixed). */
  zoneId: string;
  /** Existing vnet — undefined for create. */
  existing?: SdnVnet;
}

export function SdnVnetEditor({ open, onOpenChange, zoneId, existing }: SdnVnetEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: SdnVnetFormValues = {
    zone: existing?.zone ?? zoneId,
    alias: existing?.alias ?? "",
    tag: existing?.tag ?? 0,
    vlanAware: existing?.vlanAware ?? false,
  };

  const [alias, setAlias] = useState(initial.alias);
  const [tag, setTag] = useState(initial.tag);
  const [vlanAware, setVlanAware] = useState(initial.vlanAware);
  const [vnetId, setVnetId] = useState("");

  const isCreate = !existing;
  const target = existing ? `sdn-vnet::${existing.id}` : `sdn-vnet::${initial.zone}/${vnetId}`;
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    if (isCreate && !vnetId.trim()) {
      toast({ title: "VNet name is required", variant: "error" });
      return;
    }
    const form: SdnVnetFormValues = { zone: initial.zone, alias, tag, vlanAware };
    const op = isCreate ? buildSdnVnetCreateOp(target, form) : buildSdnVnetUpdateOp(target, initial, form);
    const label = isCreate ? vnetId : existing.id;
    submit([op], `Edit sdn vnet ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Create VNet in zone ${zoneId}` : `Edit VNet ${existing.id}`}
      description="A VNet is the virtual network guests actually attach a NIC to — it lives inside one zone."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Name" help="e.g. vnet100. Unique cluster-wide.">
          <input className={inputClass} value={vnetId} onChange={(e) => { setVnetId(e.target.value); }} placeholder="vnet100" />
        </Field>
      )}

      <Field label="Alias" help="A friendly display name, e.g. “app-tier”.">
        <input className={inputClass} value={alias} onChange={(e) => { setAlias(e.target.value); }} />
      </Field>

      <Field label="Tag" errors={findings.byField.tag} help="VLAN ID (vlan zones) or VNI (vxlan/evpn zones). Must be unique within the zone.">
        <input type="number" className={inputClass} value={tag} onChange={(e) => { setTag(Number(e.target.value)); }} />
      </Field>

      <Field label="VLAN-aware" help="Lets guests on this VNet each request their own inner VLAN tag.">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={vlanAware} onChange={(e) => { setVlanAware(e.target.checked); }} />
          VLAN aware
        </label>
      </Field>
    </EditorDialog>
  );
}
