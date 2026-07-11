// Bridge editor (docs/features/change-management.md §5): kind (Linux/OVS),
// ports multi-select with conflict hints, VLAN-aware toggle with VID range
// editor, addresses, MTU, STP, comment.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { EntityDetail, VidRange } from "../../api/types";
import { NextFreePicker } from "../../ipam/NextFreePicker";
import { useIpamSubnetsQuery } from "../../ipam/queries";
import { capsForNode, missingCapTooltip } from "../capabilities";
import type { CandidateNode } from "../entityCandidates";
import { fieldBool, fieldNum, fieldStr, fieldStrArray } from "./fieldGetters";
import { buildBridgeCreateOp, buildBridgeUpdateOp, type BridgeFormValues } from "../opBuilders";
import { refId } from "../opSummary";
import { EditorDialog, Field, inputClass } from "./EditorDialog";
import { useEditorSubmit } from "./useEditorSubmit";

/** T-405 acceptance criterion 4's proof-of-interface: the same
 * NextFreePicker component the IPAM page's reserve dialog uses, dropped
 * into this unrelated form. Lets the operator pick a known SDN subnet and
 * append its next free address to the bridge's Addresses field, instead of
 * having to cross-reference the IPAM page by hand. */
function BridgeAddressSuggest({ onAppend }: { onAppend: (cidr: string) => void }) {
  const { data: subnets } = useIpamSubnetsQuery();
  const [subnetCidr, setSubnetCidr] = useState<string | undefined>(undefined);
  const options = subnets?.items.filter((s) => s.source === "sdn") ?? [];
  if (options.length === 0) {
    return null;
  }
  const prefix = subnetCidr?.split("/")[1];
  return (
    <div className="mt-1 flex items-center gap-2">
      <select
        className={inputClass}
        value={subnetCidr ?? ""}
        onChange={(e) => {
          setSubnetCidr(e.target.value || undefined);
        }}
      >
        <option value="">Suggest from a subnet…</option>
        {options.map((s) => (
          <option key={s.cidr} value={s.cidr}>
            {s.cidr}
          </option>
        ))}
      </select>
      <NextFreePicker
        subnetCidr={subnetCidr}
        onPick={(ip) => {
          onAppend(prefix ? `${ip}/${prefix}` : ip);
        }}
      />
    </div>
  );
}

export interface BridgeEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  /** Existing bridge Ref string ("bridge:<node>:<id>" or
   * "ovs-bridge:<node>:<id>") — undefined for create. */
  target?: string;
  existing?: EntityDetail;
  newBridgeId?: string;
  candidatePorts: CandidateNode[];
}

function parseVidRanges(text: string): VidRange[] {
  return text
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s): VidRange => {
      const parts = s.split("-");
      const lo = Number((parts[0] ?? "").trim());
      const hi = Number((parts[1] ?? parts[0] ?? "").trim());
      return { low: lo, high: Number.isFinite(hi) ? hi : lo };
    })
    .filter((r) => Number.isFinite(r.low) && r.low >= 1 && r.low <= 4094);
}

function formatVidRanges(vids: VidRange[]): string {
  return vids.map((v) => (v.low === v.high ? String(v.low) : `${String(v.low)}-${String(v.high)}`)).join(", ");
}

export function BridgeEditor({ open, onOpenChange, node, target, existing, newBridgeId, candidatePorts }: BridgeEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: BridgeFormValues = {
    ports: fieldStrArray(existing?.fields, "portNames"),
    vlanAware: fieldBool(existing?.fields, "vlanAware"),
    vids: parseVidRanges(fieldStr(existing?.fields, "vids")),
    addresses: fieldStrArray(existing?.fields, "addresses"),
    gateway: fieldStr(existing?.fields, "gateway"),
    mtu: fieldNum(existing?.fields, "mtu", 1500),
    stp: fieldBool(existing?.fields, "stp"),
    comments: fieldStr(existing?.fields, "comments"),
  };

  const [ports, setPorts] = useState<string[]>(initial.ports);
  const [vlanAware, setVlanAware] = useState(initial.vlanAware);
  const [vidsText, setVidsText] = useState(formatVidRanges(initial.vids));
  const [addressesText, setAddressesText] = useState(initial.addresses.join(", "));
  const [gateway, setGateway] = useState(initial.gateway);
  const [mtu, setMtu] = useState(initial.mtu);
  const [stp, setStp] = useState(initial.stp);
  const [comments, setComments] = useState(initial.comments);
  const [bridgeId, setBridgeId] = useState(newBridgeId ?? "");

  const caps = capsForNode(session, node);
  const disabledReason = missingCapTooltip(session, node, "netWrite");
  const isCreate = !target;
  const bridgeTarget = target ?? `bridge:${node}:${bridgeId}`;

  function togglePort(name: string): void {
    setPorts((prev) => (prev.includes(name) ? prev.filter((p) => p !== name) : [...prev, name]));
  }

  function handleSubmit(): void {
    const form: BridgeFormValues = {
      ports,
      vlanAware,
      vids: parseVidRanges(vidsText),
      addresses: addressesText
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
      gateway,
      mtu,
      stp,
      comments,
    };
    if (isCreate && !bridgeId.trim()) {
      toast({ title: "Bridge name is required", variant: "error" });
      return;
    }
    const op = isCreate ? buildBridgeCreateOp(bridgeTarget, form) : buildBridgeUpdateOp(bridgeTarget, initial, form);
    submit([op], `Edit ${bridgeId || (target ? refId(target) : "bridge")}`, bridgeTarget, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Create bridge on ${node}` : `Edit bridge ${target ? refId(target) : ""}`}
      description="Linux/OVS bridges connect ports and guest NICs into one L2 segment."
      onSubmit={handleSubmit}
      disabledReason={!caps.netWrite ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Name" help="e.g. vmbr1. Bridge names conventionally start with vmbr.">
          <input className={inputClass} value={bridgeId} onChange={(e) => { setBridgeId(e.target.value); }} placeholder="vmbr1" />
        </Field>
      )}

      <Field label="Ports" errors={findings.byField.ports} help="Interfaces enslaved to this bridge. A NIC already used elsewhere shows a conflict hint.">
        <div className="max-h-32 space-y-1 overflow-y-auto rounded border border-slate-200 p-2 dark:border-slate-700">
          {candidatePorts.length === 0 && <p className="text-xs text-slate-400">No candidate interfaces found on {node}.</p>}
          {candidatePorts.map((c) => (
            <label key={c.name} className="flex items-center gap-2 text-xs">
              <input type="checkbox" checked={ports.includes(c.name)} onChange={() => { togglePort(c.name); }} />
              {c.label}
              {c.alreadyEnslaved && (
                <span className="text-amber-600 dark:text-amber-400">— already on {c.alreadyEnslaved}</span>
              )}
            </label>
          ))}
        </div>
      </Field>

      <Field label="VLAN-aware" help="Enable to trunk multiple VLANs over this bridge (required for SDN VLAN zones and per-guest tagging).">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={vlanAware} onChange={(e) => { setVlanAware(e.target.checked); }} />
          VLAN aware
        </label>
      </Field>

      {vlanAware && (
        <Field label="Allowed VID range(s)" errors={findings.byField.vids} help="Comma-separated, e.g. 10-30, 100. Each single ID is low=high.">
          <input className={inputClass} value={vidsText} onChange={(e) => { setVidsText(e.target.value); }} placeholder="10-30, 100" />
        </Field>
      )}

      <Field label="Addresses" errors={findings.byField.addresses} help="Comma-separated CIDRs, e.g. 10.10.0.11/24. Leave blank for an unmanaged bridge.">
        <input className={inputClass} value={addressesText} onChange={(e) => { setAddressesText(e.target.value); }} />
        <BridgeAddressSuggest
          onAppend={(cidr) => {
            setAddressesText((prev) => (prev.trim() ? `${prev}, ${cidr}` : cidr));
          }}
        />
      </Field>

      <Field label="Gateway" errors={findings.byField.gateway}>
        <input className={inputClass} value={gateway} onChange={(e) => { setGateway(e.target.value); }} />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="MTU" errors={findings.byField.mtu} help="576–9216. Must not exceed the smallest port's MTU.">
          <input type="number" className={inputClass} value={mtu} onChange={(e) => { setMtu(Number(e.target.value)); }} />
        </Field>
        <Field label="STP">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={stp} onChange={(e) => { setStp(e.target.checked); }} />
            Spanning tree
          </label>
        </Field>
      </div>

      <Field label="Comment">
        <input className={inputClass} value={comments} onChange={(e) => { setComments(e.target.value); }} />
      </Field>
    </EditorDialog>
  );
}
