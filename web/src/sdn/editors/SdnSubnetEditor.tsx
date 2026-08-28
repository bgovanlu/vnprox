// SPDX-License-Identifier: Apache-2.0

// SDN subnet editor: CIDR (create-only — resizing is a delete+create,
// params_sdn.go's doc comment), gateway, DNS zone prefix, DHCP ranges, SNAT.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { SdnSubnet } from "../../api/types";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";
import {
  buildSdnSubnetCreateOp,
  buildSdnSubnetUpdateOp,
  type SdnSubnetFormValues,
} from "../../changesets/opBuilders";
import { useEditorSubmit } from "../../changesets/editors/useEditorSubmit";
import { EditorDialog, Field, inputClass } from "../../changesets/editors/EditorDialog";

export interface SdnSubnetEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Owning vnet id ("<zone>/<vnet>") — create-time only. */
  vnetId: string;
  /** Existing subnet — undefined for create. */
  existing?: SdnSubnet;
}

function parseDHCPRanges(text: string): string[] {
  return text.split(",").map((s) => s.trim()).filter(Boolean);
}

function formatDHCPRanges(existing?: SdnSubnet): string {
  if (!existing?.dhcpRangeStart || !existing.dhcpRangeEnd) return "";
  return `${existing.dhcpRangeStart}-${existing.dhcpRangeEnd}`;
}

export function SdnSubnetEditor({ open, onOpenChange, vnetId, existing }: SdnSubnetEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: SdnSubnetFormValues = {
    vnet: existing?.vnet ?? vnetId,
    cidr: existing?.cidr ?? "",
    gateway: existing?.gateway ?? "",
    dnsZonePrefix: "",
    dhcpRanges: parseDHCPRanges(formatDHCPRanges(existing)),
    snat: existing?.snat ?? false,
  };

  const [cidr, setCidr] = useState(initial.cidr);
  const [gateway, setGateway] = useState(initial.gateway);
  const [dnsZonePrefix, setDnsZonePrefix] = useState(initial.dnsZonePrefix);
  const [dhcpRangesText, setDhcpRangesText] = useState(initial.dhcpRanges.join(", "));
  const [snat, setSnat] = useState(initial.snat);

  const isCreate = !existing;
  const target = existing ? `sdn-subnet::${existing.id}` : `sdn-subnet::${cidr}`;
  const disabledReason = missingCapTooltip(session, "", "sdnWrite");

  function handleSubmit(): void {
    if (isCreate && !cidr.trim()) {
      toast({ title: "CIDR is required", variant: "error" });
      return;
    }
    const form: SdnSubnetFormValues = {
      vnet: initial.vnet,
      cidr,
      gateway,
      dnsZonePrefix,
      dhcpRanges: parseDHCPRanges(dhcpRangesText),
      snat,
    };
    const op = isCreate ? buildSdnSubnetCreateOp(target, form) : buildSdnSubnetUpdateOp(target, initial, form);
    const label = isCreate ? cidr : existing.id;
    submit([op], `Edit sdn subnet ${label}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Create subnet in VNet ${vnetId}` : `Edit subnet ${existing.id}`}
      description="The IP range guests on this VNet draw addresses from."
      onSubmit={handleSubmit}
      disabledReason={!hasAnyCap(session, "sdnWrite") ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="CIDR" errors={findings.byField.addresses} help="e.g. 10.10.0.0/24.">
          <input className={inputClass} value={cidr} onChange={(e) => { setCidr(e.target.value); }} placeholder="10.10.0.0/24" />
        </Field>
      )}

      <Field label="Gateway" errors={findings.byField.gateway} help="Usually the subnet's first usable address.">
        <input className={inputClass} value={gateway} onChange={(e) => { setGateway(e.target.value); }} placeholder="10.10.0.1" />
      </Field>

      <Field label="DNS zone prefix" help="Prepended to guest hostnames in this subnet's DNS zone, if PVE's built-in DNS is configured.">
        <input className={inputClass} value={dnsZonePrefix} onChange={(e) => { setDnsZonePrefix(e.target.value); }} />
      </Field>

      <Field label="DHCP range(s)" help="Comma-separated start-end pairs, e.g. 10.10.0.100-10.10.0.200.">
        <input className={inputClass} value={dhcpRangesText} onChange={(e) => { setDhcpRangesText(e.target.value); }} placeholder="10.10.0.100-10.10.0.200" />
      </Field>

      <Field label="SNAT" help="Masquerade outbound traffic from this subnet behind the node's own address — lets an isolated subnet still reach the internet.">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={snat} onChange={(e) => { setSnat(e.target.checked); }} />
          Enable SNAT
        </label>
      </Field>
    </EditorDialog>
  );
}
