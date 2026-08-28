// SPDX-License-Identifier: Apache-2.0

// VLAN sub-interface editor (docs/features/change-management.md §5): parent
// picker, VID, addresses, MTU (warn when exceeding parent). Re-parenting or
// re-tagging an existing VLAN iface is out of scope (params_vlan.go's doc
// comment: that's a delete+create, same as NIC renames) — the parent/VID
// fields are therefore only editable at create time.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { EntityDetail, VidRange } from "../../api/types";
import { AddressSuggest } from "../../ipam/AddressSuggest";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { fieldNum, fieldStr, fieldStrArray } from "./fieldGetters";
import { buildVlanCreateOp, buildVlanUpdateOp, type VlanFormValues } from "../opBuilders";
import { refId } from "../opSummary";
import { EditorDialog, Field, inputClass } from "./EditorDialog";
import { useEditorSubmit } from "./useEditorSubmit";

function parseVidRangeList(text: string): VidRange[] {
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

export interface VlanEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target?: string;
  existing?: EntityDetail;
  /** Candidate parent interface names (bridges/bonds/physnics on this
   * node) — create-time only, for a plain 802.1q VLAN sub-interface. */
  candidateParents: string[];
  /** OVS bridges on `node` — create-time only, for an OVS Int Port's
   * parent picker (which must be an OVS bridge specifically). */
  candidateOVSBridges?: string[];
  /** The parent's own declared MTU, if known, for the "exceeds parent"
   * warning. */
  parentMtu?: number;
}

export function VlanEditor({
  open, onOpenChange, node, target, existing, candidateParents, candidateOVSBridges = [], parentMtu,
}: VlanEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: VlanFormValues = {
    parent: fieldStr(existing?.fields, "parentName", candidateParents[0] ?? ""),
    vid: fieldNum(existing?.fields, "vid", 0),
    addresses: fieldStrArray(existing?.fields, "addresses"),
    mtu: fieldNum(existing?.fields, "mtu", 1500),
    ovs: fieldStr(existing?.fields, "virt") === "ovs",
  };

  // Kind selector (docs/features/change-management.md §5's OVS deliverable):
  // a plain 802.1q VLAN sub-interface vs. an OVS Int Port. Immutable
  // post-create, same as Bridge/BondEditor's.
  const [kind, setKind] = useState<"vlan" | "ovs-intport">(initial.ovs || target?.startsWith("ovs-bridge:") ? "ovs-intport" : "vlan");
  const isOVS = kind === "ovs-intport";
  const [parent, setParent] = useState(isOVS ? (candidateOVSBridges[0] ?? "") : initial.parent);
  const [vid, setVid] = useState(initial.vid);
  const [trunksText, setTrunksText] = useState("");
  const [addressesText, setAddressesText] = useState(initial.addresses.join(", "));
  const [mtu, setMtu] = useState(initial.mtu);
  const [vlanName, setVlanName] = useState("");

  const isCreate = !target;
  const vlanId = isOVS ? vlanName : parent && vid ? `${parent}.${String(vid)}` : "";
  const vlanTarget = target ?? `vlan:${node}:${vlanId}`;
  const disabledReason = missingCapTooltip(session, node, "netWrite");
  const exceedsParent = parentMtu !== undefined && mtu > parentMtu;
  const parentOptions = isOVS ? candidateOVSBridges : candidateParents;

  function handleSubmit(): void {
    if (isCreate && !parent) {
      toast({ title: isOVS ? "Choose an OVS bridge" : "Choose a parent", variant: "error" });
      return;
    }
    if (isCreate && isOVS && !vlanName.trim()) {
      toast({ title: "Name is required", variant: "error" });
      return;
    }
    if (isCreate && !isOVS && (vid < 1 || vid > 4094)) {
      toast({ title: "Choose a VID between 1 and 4094", variant: "error" });
      return;
    }
    if (isCreate && isOVS && vid !== 0 && (vid < 1 || vid > 4094)) {
      toast({ title: "Tag must be between 1 and 4094 (or 0 for untagged/native)", variant: "error" });
      return;
    }
    const form: VlanFormValues = {
      parent,
      vid,
      addresses: addressesText.split(",").map((s) => s.trim()).filter(Boolean),
      mtu,
      ovs: isOVS,
      trunks: isOVS ? parseVidRangeList(trunksText) : undefined,
    };
    const op = isCreate ? buildVlanCreateOp(vlanTarget, form) : buildVlanUpdateOp(vlanTarget, initial, form);
    submit([op], `Edit VLAN ${vlanId || (target ? refId(target) : "")}`, vlanTarget, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Create VLAN interface on ${node}` : `Edit VLAN ${target ? refId(target) : ""}`}
      description="A tagged sub-interface on a bridge/bond/NIC (e.g. vmbr0.20), or an OVS Int Port on an OVS bridge."
      onSubmit={handleSubmit}
      disabledReason={!capsForNode(session, node).netWrite ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Kind" help="A plain VLAN sub-interface tags one parent NIC/bond/bridge. An OVS Int Port lives inside an OVS bridge instead.">
          <select
            className={inputClass}
            value={kind}
            onChange={(e) => {
              const next = e.target.value === "ovs-intport" ? "ovs-intport" : "vlan";
              setKind(next);
              setParent(next === "ovs-intport" ? (candidateOVSBridges[0] ?? "") : (candidateParents[0] ?? ""));
            }}
          >
            <option value="vlan">VLAN sub-interface</option>
            <option value="ovs-intport">OVS Int Port</option>
          </select>
        </Field>
      )}

      {isCreate && (
        <>
          <Field
            label={isOVS ? "OVS bridge" : "Parent interface"}
            errors={findings.byField.parent}
            help={
              isOVS
                ? "The OVS bridge this internal port lives inside. The port is a virtual NIC on that switch, not a child of a physical interface."
                : "The interface this VLAN rides on — a NIC, a bond, or a VLAN-aware bridge. The switch port at the other end has to be carrying this VLAN as tagged traffic, or the sub-interface comes up and carries nothing."
            }
          >
            <select className={inputClass} value={parent} onChange={(e) => { setParent(e.target.value); }}>
              <option value="" disabled>
                {isOVS ? "Choose an OVS bridge…" : "Choose a parent…"}
              </option>
              {parentOptions.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </Field>

          {isOVS && (
            <Field label="Name" help="OVS Int Ports are named freely, e.g. vlan20 (no parent.VID convention).">
              <input className={inputClass} value={vlanName} onChange={(e) => { setVlanName(e.target.value); }} placeholder="vlan20" />
            </Field>
          )}

          <Field label={isOVS ? "Tag" : "VLAN ID"} errors={findings.byField.vid} help={isOVS ? "1–4094, or 0 for untagged/native." : "1–4094."}>
            <input type="number" className={inputClass} value={vid} onChange={(e) => { setVid(Number(e.target.value)); }} />
          </Field>

          {isOVS && (
            <Field label="Trunks" errors={findings.byField.trunks} help="Comma-separated VID range(s), e.g. 10-20, 30. Optional — combine with Tag for OVS's native-tagged/native-untagged mode, or leave Tag at 0 for a pure trunk port.">
              <input className={inputClass} value={trunksText} onChange={(e) => { setTrunksText(e.target.value); }} placeholder="10-20, 30" />
            </Field>
          )}
        </>
      )}

      <Field label="Addresses" errors={findings.byField.addresses} help="Comma-separated CIDRs, e.g. 10.10.20.11/24.">
        <input className={inputClass} value={addressesText} onChange={(e) => { setAddressesText(e.target.value); }} />
        <AddressSuggest
          onAppend={(cidr) => {
            setAddressesText((prev) => (prev.trim() ? `${prev}, ${cidr}` : cidr));
          }}
        />
      </Field>

      <Field
        label="MTU"
        errors={findings.byField.mtu}
        help={
          exceedsParent
            ? `A VLAN cannot carry a larger frame than the interface it rides on. Warning: this exceeds the parent's MTU (${String(parentMtu)}) — traffic at that size may be dropped.`
            : "The largest frame this VLAN will carry. It cannot exceed the parent interface's MTU; leave at 1500 unless the whole path is set for jumbo frames."
        }
      >
        <input
          type="number"
          className={inputClass + (exceedsParent ? " border-amber-400" : "")}
          value={mtu}
          onChange={(e) => { setMtu(Number(e.target.value)); }}
        />
      </Field>
    </EditorDialog>
  );
}
