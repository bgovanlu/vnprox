// Bond editor (docs/features/change-management.md §5): mode selector with
// live guidance, slave picker showing current link state/speed of
// candidates, LACP options (rate, hash policy), MII monitor interval.
//
// "Post-apply, the inspector shows live LACP partner state (actor/partner
// system, port state flags) parsed from /proc/net/bonding/*" was a
// *read-side* requirement satisfied without any change here: InspectorPanel
// (T-107) already renders every key in an entity's `fields` generically
// (fieldRows), and once a Bond entity's fields carried that data (a host
// collector concern, not this editor's) it would appear automatically. T-804
// supplied that data (internal/host/bonding.go's actor/partner parser) and
// additionally gave it a dedicated, purpose-built rendering — the bond
// inspector's LACP tab (web/src/topology/BondLacpSection.tsx) — rather than
// relying solely on the generic Fields tab's raw JSON dump of SlaveDetail.
import { useState } from "react";
import { useSession } from "../../api/useSession";
import { useToast } from "../../components/Toast";
import type { EntityDetail } from "../../api/types";
import { capsForNode, missingCapTooltip } from "../capabilities";
import { fieldNum, fieldStr, fieldStrArray } from "./fieldGetters";
import { buildBondCreateOp, buildBondUpdateOp, type BondFormValues } from "../opBuilders";
import { refId } from "../opSummary";
import { EditorDialog, Field, inputClass } from "./EditorDialog";
import { useEditorSubmit } from "./useEditorSubmit";

export interface SlaveCandidate {
  name: string;
  label: string;
  linkUp: boolean;
  speedMbps: number;
  alreadyEnslaved?: string;
}

const MODE_GUIDANCE: Record<string, string> = {
  "802.3ad": "LACP — use this when the upstream switch also speaks LACP (link aggregation on both ends).",
  "active-backup": "Use this when the switch side does NOT support LACP — only one slave is active at a time.",
  "balance-xor": "Static hashing across slaves with no switch-side protocol — needs matching static-LAG switch config.",
  broadcast: "Transmits everything on every slave — rare, only for specific fault-tolerance needs.",
};

export interface BondEditorProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: string;
  target?: string;
  existing?: EntityDetail;
  newBondId?: string;
  candidateSlaves: SlaveCandidate[];
  /** OVS bridges on `node`, for the "attach to which OVS bridge" picker
   * shown when kind is OVS (create-only). */
  candidateOVSBridges?: string[];
}

/** OVS bond modes are a materially different vocabulary from the Linux
 * bonding driver's (internal/change's validOVSBondModes doc comment). */
const OVS_BOND_MODES = ["active-backup", "balance-slb", "balance-tcp", "802.3ad"];

export function BondEditor({
  open, onOpenChange, node, target, existing, newBondId, candidateSlaves, candidateOVSBridges = [],
}: BondEditorProps) {
  const { data: session } = useSession();
  const { toast } = useToast();
  const { findings, submit } = useEditorSubmit();

  const initial: BondFormValues = {
    mode: fieldStr(existing?.fields, "mode", "802.3ad"),
    slaves: fieldStrArray(existing?.fields, "declaredSlaves"),
    lacpRate: fieldStr(existing?.fields, "lacpRate", "slow"),
    xmitHashPolicy: fieldStr(existing?.fields, "xmitHashPolicy", "layer2"),
    miimon: fieldNum(existing?.fields, "miimon", 100),
    mtu: fieldNum(existing?.fields, "mtu", 1500),
    comments: fieldStr(existing?.fields, "comments"),
    ovsBridge: fieldStr(existing?.fields, "ovsBridge"),
  };

  // Kind selector (docs/features/change-management.md §5's OVS deliverable).
  // Immutable post-create, same as BridgeEditor's.
  const [kind, setKind] = useState<"linux" | "ovs">(target?.startsWith("ovs-bond:") ? "ovs" : "linux");
  const [mode, setMode] = useState(initial.mode);
  const [slaves, setSlaves] = useState<string[]>(initial.slaves);
  const [lacpRate, setLacpRate] = useState(initial.lacpRate);
  const [xmitHashPolicy, setXmitHashPolicy] = useState(initial.xmitHashPolicy);
  const [miimon, setMiimon] = useState(initial.miimon);
  const [mtu, setMtu] = useState(initial.mtu);
  const [comments, setComments] = useState(initial.comments);
  const [bondId, setBondId] = useState(newBondId ?? "");
  const initialOvsBridge = initial.ovsBridge ?? "";
  const [ovsBridge, setOvsBridge] = useState(initialOvsBridge !== "" ? initialOvsBridge : (candidateOVSBridges[0] ?? ""));

  const isCreate = !target;
  const bondTarget = target ?? `${kind === "ovs" ? "ovs-bond" : "bond"}:${node}:${bondId}`;
  const disabledReason = missingCapTooltip(session, node, "netWrite");
  const modeOptions = kind === "ovs" ? OVS_BOND_MODES : Object.keys(MODE_GUIDANCE);

  function toggleSlave(name: string): void {
    setSlaves((prev) => (prev.includes(name) ? prev.filter((s) => s !== name) : [...prev, name]));
  }

  function handleSubmit(): void {
    if (isCreate && !bondId.trim()) {
      toast({ title: "Bond name is required", variant: "error" });
      return;
    }
    if (slaves.length < 1) {
      toast({ title: "Select at least one slave interface", variant: "error" });
      return;
    }
    if (isCreate && kind === "ovs" && !ovsBridge.trim()) {
      toast({ title: "Choose the OVS bridge this bond attaches to", variant: "error" });
      return;
    }
    const form: BondFormValues = { mode, slaves, lacpRate, xmitHashPolicy, miimon, mtu, comments, ovsBridge };
    const op = isCreate ? buildBondCreateOp(bondTarget, form) : buildBondUpdateOp(bondTarget, initial, form);
    submit([op], `Edit ${bondId || (target ? refId(target) : "bond")}`, bondTarget, () => {
      onOpenChange(false);
    });
  }

  return (
    <EditorDialog
      open={open}
      onOpenChange={onOpenChange}
      title={isCreate ? `Create bond on ${node}` : `Edit bond ${target ? refId(target) : ""}`}
      description="Aggregates two or more physical NICs into one logical link."
      onSubmit={handleSubmit}
      disabledReason={!capsForNode(session, node).netWrite ? disabledReason : undefined}
      generalErrors={findings.general}
    >
      {isCreate && (
        <Field label="Kind" help="Linux bonds use the kernel bonding driver. OVS bonds are Open vSwitch's own bonding, attached to one OVS bridge.">
          <select className={inputClass} value={kind} onChange={(e) => { setKind(e.target.value === "ovs" ? "ovs" : "linux"); setMode(e.target.value === "ovs" ? "active-backup" : "802.3ad"); }}>
            <option value="linux">Linux bond</option>
            <option value="ovs">OVS bond</option>
          </select>
        </Field>
      )}

      {isCreate && (
        <Field label="Name" help="e.g. bond1.">
          <input className={inputClass} value={bondId} onChange={(e) => { setBondId(e.target.value); }} placeholder="bond1" />
        </Field>
      )}

      {isCreate && kind === "ovs" && (
        <Field label="OVS bridge" errors={findings.byField.bridge} help="The OVS bridge this bond attaches to (ovs_bridge).">
          <select className={inputClass} value={ovsBridge} onChange={(e) => { setOvsBridge(e.target.value); }}>
            <option value="" disabled>
              Choose an OVS bridge…
            </option>
            {candidateOVSBridges.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
        </Field>
      )}

      <Field label="Mode" errors={findings.byField.mode} help={kind === "linux" ? MODE_GUIDANCE[mode] : undefined}>
        <select className={inputClass} value={mode} onChange={(e) => { setMode(e.target.value); }}>
          {modeOptions.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Slaves" errors={findings.byField.slaves} help="Candidates show current link state/speed. A NIC already enslaved elsewhere shows a conflict hint.">
        <div className="max-h-32 space-y-1 overflow-y-auto rounded border border-slate-200 p-2 dark:border-slate-700">
          {candidateSlaves.length === 0 && <p className="text-xs text-slate-600 dark:text-slate-400">No candidate physical NICs found on {node}.</p>}
          {candidateSlaves.map((c) => (
            <label key={c.name} className="flex items-center gap-2 text-xs">
              <input type="checkbox" checked={slaves.includes(c.name)} onChange={() => { toggleSlave(c.name); }} />
              {c.label}
              <span className={c.linkUp ? "text-emerald-600 dark:text-emerald-400" : "text-red-500"}>
                {c.linkUp ? `up, ${String(c.speedMbps)}Mbps` : "down"}
              </span>
              {c.alreadyEnslaved && <span className="text-amber-600 dark:text-amber-400">— already on {c.alreadyEnslaved}</span>}
            </label>
          ))}
        </div>
      </Field>

      {kind === "linux" && mode === "802.3ad" && (
        <div className="grid grid-cols-2 gap-3">
          <Field
            label="LACP rate"
            errors={findings.byField.lacpRate}
            help="How often the two ends exchange LACP packets. `fast` detects a dead link in about 3 seconds instead of about 90, at the cost of one small packet per second per link — but **both ends must agree**, and a switch left on `slow` against a host on `fast` renegotiates constantly. Leave it on `slow` unless you have set the switch side too."
          >
            <select className={inputClass} value={lacpRate} onChange={(e) => { setLacpRate(e.target.value); }}>
              <option value="slow">slow (30s)</option>
              <option value="fast">fast (1s)</option>
            </select>
          </Field>
          <Field label="Hash policy" errors={findings.byField.xmitHashPolicy} help="layer3+4 spreads load best for most workloads.">
            <select className={inputClass} value={xmitHashPolicy} onChange={(e) => { setXmitHashPolicy(e.target.value); }}>
              <option value="layer2">layer2</option>
              <option value="layer2+3">layer2+3</option>
              <option value="layer3+4">layer3+4</option>
            </select>
          </Field>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        {kind === "linux" && (
          <Field label="MII monitor (ms)" errors={findings.byField.miimon} help="How often link state is polled.">
            <input type="number" className={inputClass} value={miimon} onChange={(e) => { setMiimon(Number(e.target.value)); }} />
          </Field>
        )}
        <Field
          label="MTU"
          errors={findings.byField.mtu}
          help="The largest frame this bond will carry. It cannot usefully exceed the smallest member NIC's MTU — the bond will accept the number and the traffic will drop on whichever member cannot carry it. Leave at 1500 unless every switch port in the path is set for jumbo frames too."
        >
          <input type="number" className={inputClass} value={mtu} onChange={(e) => { setMtu(Number(e.target.value)); }} />
        </Field>
      </div>

      <Field
        label="Comment"
        help="A free-text note written into the interfaces file beside this bond. Useful for the thing the config cannot say — which switch stack the members land on, or which ticket asked for it."
      >
        <input className={inputClass} value={comments} onChange={(e) => { setComments(e.target.value); }} />
      </Field>
    </EditorDialog>
  );
}
