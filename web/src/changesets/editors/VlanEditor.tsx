// VLAN sub-interface editor (docs/features/change-management.md §5): parent
// picker, VID, addresses, MTU (warn when exceeding parent). Re-parenting or
// re-tagging an existing VLAN iface is out of scope (params_vlan.go's doc
// comment: that's a delete+create, same as NIC renames) — the parent/VID
// fields are therefore only editable at create time.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { EntityDetail } from "../../api/types";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { fieldNum, fieldStr, fieldStrArray } from "./fieldGetters";
import { buildVlanCreateOp, buildVlanUpdateOp, type VlanFormValues } from "../opBuilders";
import { refId } from "../opSummary";
import { EditorDialog, Field, inputClass } from "./EditorDialog";
import { useEditorSubmit } from "./useEditorSubmit";

export interface VlanEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target?: string;
  existing?: EntityDetail;
  /** Candidate parent interface names (bridges/bonds/physnics on this
   * node) — create-time only. */
  candidateParents: string[];
  /** The parent's own declared MTU, if known, for the "exceeds parent"
   * warning. */
  parentMtu?: number;
}

export function VlanEditor({ open, onOpenChange, node, target, existing, candidateParents, parentMtu }: VlanEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: VlanFormValues = {
    parent: fieldStr(existing?.fields, "parentName", candidateParents[0] ?? ""),
    vid: fieldNum(existing?.fields, "vid", 0),
    addresses: fieldStrArray(existing?.fields, "addresses"),
    mtu: fieldNum(existing?.fields, "mtu", 1500),
  };

  const [parent, setParent] = useState(initial.parent);
  const [vid, setVid] = useState(initial.vid);
  const [addressesText, setAddressesText] = useState(initial.addresses.join(", "));
  const [mtu, setMtu] = useState(initial.mtu);

  const isCreate = !target;
  const vlanId = parent && vid ? `${parent}.${String(vid)}` : "";
  const vlanTarget = target ?? `vlan:${node}:${vlanId}`;
  const disabledReason = missingCapTooltip(session, node, "netWrite");
  const exceedsParent = parentMtu !== undefined && mtu > parentMtu;

  function handleSubmit(): void {
    if (isCreate && (!parent || vid < 1 || vid > 4094)) {
      toast({ title: "Choose a parent and a VID between 1 and 4094", variant: "error" });
      return;
    }
    const form: VlanFormValues = {
      parent,
      vid,
      addresses: addressesText.split(",").map((s) => s.trim()).filter(Boolean),
      mtu,
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
      description="A tagged sub-interface on a bridge/bond/NIC, e.g. vmbr0.20."
      onSubmit={handleSubmit}
      disabledReason={!capsForNode(session, node).netWrite ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <>
          <Field label="Parent interface" errors={findings.byField.parent}>
            <select className={inputClass} value={parent} onChange={(e) => { setParent(e.target.value); }}>
              <option value="" disabled>
                Choose a parent…
              </option>
              {candidateParents.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </Field>
          <Field label="VLAN ID" errors={findings.byField.vid} help="1–4094.">
            <input type="number" className={inputClass} value={vid} onChange={(e) => { setVid(Number(e.target.value)); }} />
          </Field>
        </>
      )}

      <Field label="Addresses" errors={findings.byField.addresses} help="Comma-separated CIDRs, e.g. 10.10.20.11/24.">
        <input className={inputClass} value={addressesText} onChange={(e) => { setAddressesText(e.target.value); }} />
      </Field>

      <Field
        label="MTU"
        errors={findings.byField.mtu}
        help={exceedsParent ? `Warning: exceeds the parent's MTU (${String(parentMtu)}) — traffic may be dropped.` : undefined}
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
