// Physical-interface editor (docs/features/change-management.md §5): MTU,
// addresses/gateway, autostart, comment. "Renaming NICs is out of scope v1
// ... document the manual procedure instead" — done via the help text
// below rather than any UI affordance to rename.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import type { EntityDetail } from "../../api/types";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { fieldBool, fieldNum, fieldStr, fieldStrArray } from "./fieldGetters";
import { buildIfaceUpdateOp, type IfaceFormValues } from "../opBuilders";
import { refId } from "../opSummary";
import { EditorDialog, Field, FieldHelp, inputClass } from "./EditorDialog";
import { useEditorSubmit } from "./useEditorSubmit";

export interface InterfaceEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target: string;
  existing?: EntityDetail;
}

export function InterfaceEditor({ open, onOpenChange, node, target, existing }: InterfaceEditorProps) {
  const { data: session } = useSession();
  const { findings, submit } = useEditorSubmit();

  const initial: IfaceFormValues = {
    mtu: fieldNum(existing?.fields, "mtu", 1500),
    comments: fieldStr(existing?.fields, "comments"),
    addresses: fieldStrArray(existing?.fields, "addresses"),
    gateway: fieldStr(existing?.fields, "gateway"),
    autostart: fieldBool(existing?.fields, "autostart", true),
  };

  const [mtu, setMtu] = useState(initial.mtu);
  const [comments, setComments] = useState(initial.comments);
  const [addressesText, setAddressesText] = useState(initial.addresses.join(", "));
  const [gateway, setGateway] = useState(initial.gateway);
  const [autostart, setAutostart] = useState(initial.autostart);

  const disabledReason = missingCapTooltip(session, node, "netWrite");

  function handleSubmit(): void {
    const form: IfaceFormValues = {
      mtu,
      comments,
      addresses: addressesText.split(",").map((s) => s.trim()).filter(Boolean),
      gateway,
      autostart,
    };
    const op = buildIfaceUpdateOp(target, initial, form);
    submit([op], `Edit ${refId(target)}`, target, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Edit interface ${refId(target)}`}
      description={`Physical NIC on ${node}.`}
      onSubmit={handleSubmit}
      disabledReason={!capsForNode(session, node).netWrite ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      <Field
        label="MTU"
        errors={findings.byField.mtu}
        help="The largest frame this NIC will carry. Every device in the path — the switch port, and anything this NIC is enslaved to — has to agree, so raising it here alone usually produces traffic that works for small packets and stalls for large ones."
      >
        <input type="number" className={inputClass} value={mtu} onChange={(e) => { setMtu(Number(e.target.value)); }} />
      </Field>
      <Field label="Addresses" errors={findings.byField.addresses} help="Comma-separated CIDRs. Usually empty for a NIC that's a bond/bridge member.">
        <input className={inputClass} value={addressesText} onChange={(e) => { setAddressesText(e.target.value); }} />
      </Field>
      <Field
        label="Gateway"
        errors={findings.byField.gateway}
        help="The router this node uses for traffic leaving its subnet. Set it on exactly one interface per address family — usually the management bridge, not a bare NIC."
      >
        <input className={inputClass} value={gateway} onChange={(e) => { setGateway(e.target.value); }} />
      </Field>
      <Field
        label="Autostart"
        help="Whether this interface is brought up at boot (`auto` in the interfaces file). Turning it off on an interface carrying the management path is how a node comes back from a reboot unreachable."
      >
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={autostart} onChange={(e) => { setAutostart(e.target.checked); }} />
          Bring up at boot
        </label>
      </Field>
      <Field
        label="Comment"
        help="A free-text note written into the interfaces file beside this NIC — which switch port it is patched to, for instance."
      >
        <input className={inputClass} value={comments} onChange={(e) => { setComments(e.target.value); }} />
      </Field>
      <FieldHelp>
        Renaming a physical NIC isn't supported here — udev/link-name files are hardware-specific. To rename one,
        edit its udev `.link` file on the node directly and reboot; vnprox will pick up the new name on the next poll.
      </FieldHelp>
    </EditorDialog>
  );
}
