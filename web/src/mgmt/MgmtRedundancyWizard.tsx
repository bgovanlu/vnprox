// SPDX-License-Identifier: Apache-2.0

// T-703's guided management-redundancy wizard, built on T-403's WizardShell
// step engine. Launched from the mgmt_single_path finding, the inspector's
// "Management path" section, and the topology New menu (all via
// mgmtWizardStore). Output is always a changeset draft into the standard
// drawer — never a direct apply (WizardShell's onFinish is the only place
// ops are drafted; abandoning leaves no residue since nothing is sent until
// "Add to changeset").
import { useMemo, useState } from "react";
import { useToast } from "../components/Toast";
import { Field, inputClass } from "../changesets/editors/EditorDialog";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { useInventoryDetailQuery, useMgmtStatusQuery, useTopologyQuery } from "../topology/queries";
import { useLldpQuery } from "../onboarding/queries";
import { WizardShell, type WizardStep } from "../sdn/wizards/WizardShell";
import {
  buildBondSlaveUpdateOps,
  buildBondUplinkOps,
  buildDedicatedVlanOps,
  nextFreeBondName,
  parseRef,
  type BondMode,
} from "./mgmtWizardOps";
import { analyzeMgmtSituation, physnicsOfNode, type MgmtSituation } from "./situation";
import { mgmtStrings } from "./strings";

const S = mgmtStrings;

type Flow = "bond-uplink" | "add-slave" | "dedicated-vlan";

export interface MgmtRedundancyWizardProps {
  node: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/** Reads a string field off an EntityDetail's generic fields map. */
function fieldStr(fields: Record<string, unknown> | undefined, key: string): string {
  const v = fields?.[key];
  return typeof v === "string" ? v : "";
}

export function MgmtRedundancyWizard({ node, open, onOpenChange }: MgmtRedundancyWizardProps) {
  const { toast } = useToast();
  const { addOps } = useDrawerActions();
  const { data: mgmtStatus } = useMgmtStatusQuery();
  const { data: topology } = useTopologyQuery();
  const { data: lldp } = useLldpQuery();

  const situation: MgmtSituation = useMemo(() => {
    const nodePaths = mgmtStatus?.nodes[node] ?? [];
    return analyzeMgmtSituation(nodePaths, topology?.nodes ?? [], node);
  }, [mgmtStatus, topology, node]);

  // Carrier detail (flow C needs its address/gateway; VLAN carriers need
  // their parent bridge). Enabled only when a carrier is resolved.
  const { data: carrierDetail } = useInventoryDetailQuery(situation.carrierRef);

  const availableFlows = useMemo<Flow[]>(() => {
    const flows: Flow[] = [];
    if (situation.canBondUplink) flows.push("bond-uplink");
    if (situation.canAddSlave) flows.push("add-slave");
    if (situation.canDedicatedVlan) flows.push("dedicated-vlan");
    return flows;
  }, [situation]);

  const [flow, setFlow] = useState<Flow | undefined>(undefined);
  const activeFlow = flow ?? availableFlows[0];

  // Flow A/B state.
  const [candidateNic, setCandidateNic] = useState("");
  const [bondMode, setBondMode] = useState<BondMode>("active-backup");
  const [replaceOut, setReplaceOut] = useState("");
  const [addOrReplace, setAddOrReplace] = useState<"add" | "replace">("add");

  // Flow C state.
  const [vid, setVid] = useState<number>(0);

  const [finishing, setFinishing] = useState(false);

  const bondName = useMemo(
    () => nextFreeBondName(physnicsOfNode(topology?.nodes ?? [], node)),
    [topology, node],
  );

  // LLDP neighbour for a chosen candidate NIC (link-state / different-switch
  // hinting, flow A). Compared against the current uplink's neighbour.
  const lldpByIface = useMemo(() => {
    const map = new Map<string, { chassisName: string }>();
    for (const n of lldp?.items ?? []) {
      if (n.node === node) map.set(n.localIface, { chassisName: n.chassisName });
    }
    return map;
  }, [lldp, node]);

  const candidateLldp = candidateNic ? lldpByIface.get(candidateNic) : undefined;
  const currentUplinkNic = situation.pathNics[0];
  const currentLldp = currentUplinkNic ? lldpByIface.get(currentUplinkNic) : undefined;
  const differentSwitch =
    candidateLldp && currentLldp && candidateLldp.chassisName !== currentLldp.chassisName;

  const carrierParsed = situation.carrierRef ? parseRef(situation.carrierRef) : undefined;
  const carrierAddresses = fieldStr(carrierDetail?.fields, "addresses");
  const carrierGateway = fieldStr(carrierDetail?.fields, "gateway");
  // For a bridge carrier the parent for a new VLAN sub-interface is the
  // bridge itself; for a VLAN carrier it's that carrier's own parent bridge.
  const dedicatedParentBridge =
    carrierParsed?.kind === "vlan"
      ? fieldStr(carrierDetail?.fields, "parentName")
      : (carrierParsed?.id ?? "");
  const dedicatedAddresses = carrierAddresses ? carrierAddresses.split(",").map((s) => s.trim()).filter(Boolean) : [];

  function buildOps() {
    switch (activeFlow) {
      case "bond-uplink":
        return buildBondUplinkOps({
          node,
          bridgeRef: situation.carrierRef ?? "",
          currentPortNic: currentUplinkNic ?? "",
          candidateNic,
          bondName,
          mode: bondMode,
        });
      case "add-slave": {
        const slaves =
          addOrReplace === "add"
            ? [...situation.pathNics, candidateNic]
            : situation.pathNics.map((s) => (s === replaceOut ? candidateNic : s));
        return buildBondSlaveUpdateOps({ bondRef: situation.bondRef ?? "", slaves });
      }
      case "dedicated-vlan":
        return buildDedicatedVlanOps({
          node,
          oldCarrierRef: situation.carrierRef ?? "",
          parentBridge: dedicatedParentBridge,
          vid,
          vlanName: `${dedicatedParentBridge}.${String(vid)}`,
          addresses: dedicatedAddresses,
          gateway: carrierGateway,
        });
      default:
        return [];
    }
  }

  function handleFinish(): void {
    setFinishing(true);
    const ops = buildOps();
    const title = `Management redundancy: ${node}`;
    void addOps(ops, title)
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

  const flowLabel: Record<Flow, { card: string; blurb: string }> = {
    "bond-uplink": { card: S.flowA.card, blurb: S.flowA.blurb },
    "add-slave": { card: S.flowB.card, blurb: S.flowB.blurb },
    "dedicated-vlan": { card: S.flowC.card, blurb: S.flowC.blurb },
  };

  const configValid = (() => {
    switch (activeFlow) {
      case "bond-uplink":
        return candidateNic !== "";
      case "add-slave":
        return addOrReplace === "add" ? candidateNic !== "" : candidateNic !== "" && replaceOut !== "";
      case "dedicated-vlan":
        return vid >= 1 && vid <= 4094 && dedicatedParentBridge !== "" && dedicatedAddresses.length > 0;
      default:
        return false;
    }
  })();

  const steps: WizardStep[] = [
    {
      id: "choose",
      title: "What to do",
      isValid: availableFlows.length > 0 && activeFlow !== undefined,
      invalidReason: availableFlows.length === 0 ? S.picker.noCarrier : undefined,
      content: (
        <div className="space-y-3">
          {situation.redundant && (
            <p className="rounded border border-emerald-300 bg-emerald-50 p-2 text-xs text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200">
              {S.picker.alreadyRedundant}
            </p>
          )}
          <p className="text-slate-600 dark:text-slate-300">{S.picker.description}</p>
          <div className="space-y-2" role="radiogroup" aria-label="Redundancy option">
            {availableFlows.map((f) => (
              <label
                key={f}
                className={`flex cursor-pointer gap-2 rounded-lg border p-3 text-sm ${
                  activeFlow === f ? "border-accent-500 bg-accent-50 dark:bg-accent-950" : "border-slate-200 dark:border-slate-700"
                }`}
              >
                <input
                  type="radio"
                  name="mgmt-flow"
                  className="mt-0.5"
                  checked={activeFlow === f}
                  onChange={() => {
                    setFlow(f);
                  }}
                />
                <span>
                  <span className="font-medium text-slate-800 dark:text-slate-100">{flowLabel[f].card}</span>
                  <span className="mt-0.5 block text-xs text-slate-500 dark:text-slate-400">{flowLabel[f].blurb}</span>
                </span>
              </label>
            ))}
          </div>
        </div>
      ),
    },
    {
      id: "config",
      title: "Configure",
      isValid: configValid,
      content: <ConfigStep />,
    },
  ];

  function ConfigStep() {
    if (activeFlow === "bond-uplink") {
      return (
        <div className="space-y-3 text-sm">
          <p className="text-slate-600 dark:text-slate-300">{S.flowA.intro}</p>
          <Field label={S.flowA.pickCandidate} help={S.flowA.pickCandidateHelp}>
            <select
              className={inputClass}
              value={candidateNic}
              onChange={(e) => {
                setCandidateNic(e.target.value);
              }}
              aria-label={S.flowA.pickCandidate}
            >
              <option value="">Select a card…</option>
              {situation.candidateNics.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </Field>
          {differentSwitch && (
            <p className="rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
              {S.flowA.differentSwitchWarn}
            </p>
          )}
          <Field label={S.flowA.modeLabel} help={bondMode === "active-backup" ? S.flowA.modeActiveBackupHelp : S.flowA.modeLacpHelp}>
            <select
              className={inputClass}
              value={bondMode}
              onChange={(e) => {
                setBondMode(e.target.value as BondMode);
              }}
              aria-label={S.flowA.modeLabel}
            >
              <option value="active-backup">{S.flowA.modeActiveBackup}</option>
              <option value="802.3ad">{S.flowA.modeLacp}</option>
            </select>
          </Field>
          <Field label={S.flowA.bondNameLabel} help={S.flowA.bondNameHelp}>
            <input className={inputClass} value={bondName} readOnly />
          </Field>
        </div>
      );
    }
    if (activeFlow === "add-slave") {
      return (
        <div className="space-y-3 text-sm">
          <p className="text-slate-600 dark:text-slate-300">{S.flowB.intro}</p>
          <Field label={S.flowB.modeLabel}>
            <div className="flex gap-4">
              <label className="flex items-center gap-1.5">
                <input type="radio" name="addreplace" checked={addOrReplace === "add"} onChange={() => { setAddOrReplace("add"); }} />
                {S.flowB.addLabel}
              </label>
              <label className="flex items-center gap-1.5">
                <input type="radio" name="addreplace" checked={addOrReplace === "replace"} onChange={() => { setAddOrReplace("replace"); }} />
                {S.flowB.replaceLabel}
              </label>
            </div>
          </Field>
          {addOrReplace === "replace" && (
            <Field label={S.flowB.pickReplaceOut}>
              <select className={inputClass} value={replaceOut} onChange={(e) => { setReplaceOut(e.target.value); }} aria-label={S.flowB.pickReplaceOut}>
                <option value="">Select a card…</option>
                {situation.pathNics.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
            </Field>
          )}
          <Field label={addOrReplace === "add" ? S.flowB.pickAdd : S.flowB.pickReplaceIn}>
            <select className={inputClass} value={candidateNic} onChange={(e) => { setCandidateNic(e.target.value); }} aria-label={S.flowB.pickAdd}>
              <option value="">Select a card…</option>
              {situation.candidateNics.map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>
          </Field>
        </div>
      );
    }
    // dedicated-vlan
    return (
      <div className="space-y-3 text-sm">
        <p className="text-slate-600 dark:text-slate-300">{S.flowC.intro}</p>
        <Field label={S.flowC.vidLabel} help={S.flowC.vidHelp}>
          <input
            className={inputClass}
            type="number"
            min={1}
            max={4094}
            value={vid || ""}
            onChange={(e) => {
              setVid(Number(e.target.value));
            }}
            aria-label={S.flowC.vidLabel}
          />
        </Field>
        <p className="text-xs text-slate-500 dark:text-slate-400">
          {S.flowC.carriesAddress}: <span className="font-mono">{dedicatedAddresses.join(", ") || "(unknown)"}</span>
          {carrierGateway ? ` · gateway ${carrierGateway}` : ""} — {S.flowC.fromCarrier} {situation.carrierRef}
        </p>
      </div>
    );
  }

  return (
    <WizardShell
      open={open}
      onOpenChange={onOpenChange}
      title={S.picker.title}
      helpTopic="mgmt-redundancy-wizard"
      intro={S.picker.description}
      steps={steps}
      preview={<PreviewPanel node={node} flow={activeFlow} ops={configValid ? buildOps() : []} />}
      onFinish={handleFinish}
      finishing={finishing}
    />
  );
}

function PreviewPanel({ node, flow, ops }: { node: string; flow: Flow | undefined; ops: { op: string }[] }) {
  return (
    <div className="h-full overflow-y-auto rounded border border-slate-200 bg-slate-50 p-3 text-xs dark:border-slate-700 dark:bg-slate-900">
      <div className="font-medium text-slate-700 dark:text-slate-200">Draft for {node}</div>
      {ops.length === 0 ? (
        <p className="mt-2 text-slate-600 dark:text-slate-400">Fill in the options on the left to see the exact steps this will draft.</p>
      ) : (
        <ol className="mt-2 list-inside list-decimal space-y-1 text-slate-600 dark:text-slate-300">
          {ops.map((o, i) => (
            <li key={i}>
              <span className="rounded bg-sky-100 px-1 py-0.5 font-mono text-[10px] text-sky-700 dark:bg-sky-950 dark:text-sky-300">{o.op}</span>
            </li>
          ))}
        </ol>
      )}
      <p className="mt-3 text-[11px] text-slate-600 dark:text-slate-400">{mgmtStrings.common.draftNotice}</p>
      {flow === "dedicated-vlan" && (
        <p className="mt-1 text-[11px] text-slate-600 dark:text-slate-400">Your management address is carried over unchanged.</p>
      )}
    </div>
  );
}
