// SPDX-License-Identifier: Apache-2.0

// QoS shaping (GET /qos/shapes, T-1505): what is shaped right now, and the
// three edits an operator can make to it.
//
// Every edit here stages a `qos.shape.create|update|delete` op into the
// change drawer and stops. There is no apply, no confirm and no direct write
// on this page — QoS is the one write path in T-3004's set, so it goes
// through the change engine exactly like everything else (CLAUDE.md: "never
// apply network changes outside the change engine"). The op builders are in
// qosOps.ts; this file only collects form values and hands them to
// useDrawerActions.
import { useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useDrawerActions } from "../changesets/useDrawerActions";
import type { Op, QosShape } from "../api/types";
import { useTopologyQuery } from "../topology/queries";
import { MapLink } from "./MapLink";
import { useQosShapesQuery } from "./analysisQueries";
import {
  buildQosShapeCreateOp,
  buildQosShapeDeleteOp,
  buildQosShapeUpdateOp,
  qosShapeFormChanged,
  qosShapeRef,
  type QosShapeFormValues,
} from "./qosOps";

const DRAFT_TITLE = "QoS shaping";

/** One bridge an operator can attach a shape to, as the topology projection
 * names it: node from the pill's column, bridge from its label. */
interface BridgeOption {
  node: string;
  bridge: string;
}

function shapeToForm(shape: QosShape): QosShapeFormValues {
  return {
    bridge: shape.bridge,
    rateMbit: shape.rateMbit,
    ceilMbit: shape.ceilMbit,
    matchCidr: shape.matchCidr,
    matchVlan: shape.matchVlan,
    priority: shape.priority,
  };
}

export function QosShapesPanel() {
  const { data: shapes, isLoading, error, refetch } = useQosShapesQuery();
  const { data: session } = useSession();
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");
  const [editing, setEditing] = useState<QosShape | undefined>(undefined);

  return (
    <section aria-labelledby="qos-heading" className="flex flex-col gap-3">
      <div>
        <h2 id="qos-heading" className="flex items-center gap-2 text-lg font-semibold">
          QoS shaping
          <HelpAnchor topic="qos-shaping" />
        </h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Every shape vnprox has applied, read from its own store rather than from live <code>tc</code>. Creating,
          editing and removing a shape stages an ordinary changeset — this panel never writes anything itself.
        </p>
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {error && (
        <EmptyState
          icon="guest-nic"
          variant="failed"
          title="Could not list QoS shapes"
          description="The daemon could not read the shape store. Try again in a moment."
          density="compact"
          action={
            <Button variant="secondary" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !error && (
        <>
          {shapes && shapes.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Shape</TableHead>
                  <TableHead>Node</TableHead>
                  <TableHead>Bridge</TableHead>
                  <TableHead>Match</TableHead>
                  <TableHead>Rate</TableHead>
                  <TableHead>Ceiling</TableHead>
                  <TableHead>Priority</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {shapes.map((s) => (
                  <QosShapeRow
                    key={qosShapeRef(s.node, s.id)}
                    shape={s}
                    canWrite={canWrite}
                    writeDisabledReason={writeDisabledReason}
                    onEdit={() => {
                      setEditing(s);
                    }}
                  />
                ))}
              </TableBody>
            </Table>
          ) : (
            <EmptyState
              icon="guest-nic"
              variant="unconfigured"
              title="No QoS shapes applied"
              description="Nothing on this cluster is currently shaped. Add a shape below to stage one."
              density="compact"
            />
          )}

          {editing ? (
            <EditShapeForm
              shape={editing}
              canWrite={canWrite}
              writeDisabledReason={writeDisabledReason}
              onDone={() => {
                setEditing(undefined);
              }}
            />
          ) : (
            <CreateShapeForm canWrite={canWrite} writeDisabledReason={writeDisabledReason} />
          )}
        </>
      )}
    </section>
  );
}

function QosShapeRow({
  shape,
  canWrite,
  writeDisabledReason,
  onEdit,
}: {
  shape: QosShape;
  canWrite: boolean;
  writeDisabledReason?: string;
  onEdit: () => void;
}) {
  const stage = useStageQosOp();
  const match: string[] = [];
  if (shape.matchCidr) match.push(shape.matchCidr);
  if (shape.matchVlan !== undefined) match.push(`VLAN ${String(shape.matchVlan)}`);
  return (
    <TableRow>
      <TableCell>
        <MapLink entityRef={qosShapeRef(shape.node, shape.id)} label={shape.id} />
      </TableCell>
      <TableCell>{shape.node}</TableCell>
      <TableCell className="font-mono text-xs">{shape.bridge}</TableCell>
      <TableCell className="font-mono text-xs">{match.length > 0 ? match.join(" · ") : "whole bridge"}</TableCell>
      <TableCell className="tabular-nums">{shape.rateMbit} Mbit</TableCell>
      <TableCell className="tabular-nums">{shape.ceilMbit === undefined ? "—" : `${String(shape.ceilMbit)} Mbit`}</TableCell>
      <TableCell className="tabular-nums">{shape.priority ?? "—"}</TableCell>
      <TableCell>
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" disabled={!canWrite} title={writeDisabledReason} onClick={onEdit}>
            Edit
          </Button>
          <Button
            size="sm"
            variant="destructive"
            disabled={!canWrite}
            title={writeDisabledReason}
            onClick={() => {
              void stage(buildQosShapeDeleteOp(shape.node, shape.id), `Stage removal of QoS shape ${shape.id}`);
            }}
          >
            Remove
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

/** Lands one op in the change drawer and reports the outcome. The single
 * place this panel touches a mutation — and it is a changeset mutation, not
 * a QoS one. */
function useStageQosOp(): (op: Op, description: string) => Promise<void> {
  const { addOps } = useDrawerActions();
  const { toast } = useToast();
  return async (op, description) => {
    try {
      const changeset = await addOps([op], DRAFT_TITLE);
      toast({
        title: "Staged in the change drawer",
        description: `${description} — review and apply changeset ${changeset.id} to make it real.`,
      });
    } catch (err) {
      toast({
        title: "Could not stage the change",
        description: err instanceof Error ? err.message : String(err),
        variant: "error",
      });
    }
  };
}

/** The bridges a new shape can attach to, from the topology projection.
 * Falls back to a free-text node/bridge pair when the map has no bridge
 * nodes (a daemon whose PVE poller has not discovered anything yet) rather
 * than blocking the form entirely. */
function useBridgeOptions(): BridgeOption[] {
  const { data } = useTopologyQuery();
  return useMemo(() => {
    const out: BridgeOption[] = [];
    for (const n of data?.nodes ?? []) {
      if (n.kind === "bridge" && n.nodeGroup) {
        out.push({ node: n.nodeGroup, bridge: n.label });
      }
    }
    return out.sort((a, b) => a.node.localeCompare(b.node) || a.bridge.localeCompare(b.bridge));
  }, [data]);
}

function optionValue(o: BridgeOption): string {
  return `${o.node}/${o.bridge}`;
}

function CreateShapeForm({ canWrite, writeDisabledReason }: { canWrite: boolean; writeDisabledReason?: string }) {
  const stage = useStageQosOp();
  const bridges = useBridgeOptions();
  const [selected, setSelected] = useState("");
  const [id, setId] = useState("");
  const [rate, setRate] = useState("");
  const [ceil, setCeil] = useState("");
  const [matchCidr, setMatchCidr] = useState("");
  const [matchVlan, setMatchVlan] = useState("");

  const target = bridges.find((b) => optionValue(b) === selected) ?? bridges[0];
  const rateMbit = Number.parseInt(rate, 10);
  const ready = target !== undefined && id.trim() !== "" && Number.isFinite(rateMbit);

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    // `ready` already carries `target !== undefined`; TypeScript narrows
    // through the alias, so no second check is needed here.
    if (!ready) return;
    void stage(
      buildQosShapeCreateOp(target.node, id.trim(), {
        bridge: target.bridge,
        rateMbit,
        ceilMbit: parseOptionalInt(ceil),
        matchCidr: matchCidr.trim() === "" ? undefined : matchCidr.trim(),
        matchVlan: parseOptionalInt(matchVlan),
      }),
      `Stage QoS shape ${id.trim()} on ${target.bridge}`,
    );
  }

  if (bridges.length === 0) {
    return (
      <EmptyState
        icon="bridge"
        variant="empty"
        title="No bridges discovered yet"
        description="A QoS shape attaches to a bridge, and vnprox has not discovered one on this cluster yet. Once the topology map has bridges, this form offers them."
        density="compact"
      />
    );
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-wrap items-end gap-2" aria-label="Add QoS shape">
      <Field label="Bridge">
        <select
          value={selected === "" && target ? optionValue(target) : selected}
          onChange={(e) => {
            setSelected(e.target.value);
          }}
          disabled={!canWrite}
          className="rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
        >
          {bridges.map((b) => (
            <option key={optionValue(b)} value={optionValue(b)}>
              {b.node} / {b.bridge}
            </option>
          ))}
        </select>
      </Field>
      <TextField label="Shape id" value={id} onChange={setId} disabled={!canWrite} placeholder="guest-egress" />
      <TextField label="Rate (Mbit)" value={rate} onChange={setRate} disabled={!canWrite} placeholder="100" />
      <TextField label="Ceiling (Mbit)" value={ceil} onChange={setCeil} disabled={!canWrite} placeholder="optional" />
      <TextField label="Match CIDR" value={matchCidr} onChange={setMatchCidr} disabled={!canWrite} placeholder="optional" />
      <TextField label="Match VLAN" value={matchVlan} onChange={setMatchVlan} disabled={!canWrite} placeholder="optional" />
      <Button type="submit" size="sm" disabled={!canWrite || !ready} title={writeDisabledReason}>
        Stage shape
      </Button>
    </form>
  );
}

function EditShapeForm({
  shape,
  canWrite,
  writeDisabledReason,
  onDone,
}: {
  shape: QosShape;
  canWrite: boolean;
  writeDisabledReason?: string;
  onDone: () => void;
}) {
  const stage = useStageQosOp();
  const initial = shapeToForm(shape);
  const [rate, setRate] = useState(String(shape.rateMbit));
  const [ceil, setCeil] = useState(shape.ceilMbit === undefined ? "" : String(shape.ceilMbit));
  const [priority, setPriority] = useState(shape.priority === undefined ? "" : String(shape.priority));

  const rateMbit = Number.parseInt(rate, 10);
  const form: QosShapeFormValues = {
    ...initial,
    rateMbit: Number.isFinite(rateMbit) ? rateMbit : initial.rateMbit,
    ceilMbit: parseOptionalInt(ceil),
    priority: parseOptionalInt(priority),
  };
  const changed = qosShapeFormChanged(initial, form);

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!changed) return;
    void stage(
      buildQosShapeUpdateOp(shape.node, shape.id, initial, form),
      `Stage edit of QoS shape ${shape.id}`,
    );
    onDone();
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-wrap items-end gap-2" aria-label={`Edit QoS shape ${shape.id}`}>
      <p className="w-full text-xs text-slate-600 dark:text-slate-400">
        Editing <span className="font-mono">{shape.id}</span> on {shape.node} / {shape.bridge}. Changing which traffic a
        shape selects is a remove-and-recreate, not an edit, so the match fields are not offered here.
      </p>
      <TextField label="Rate (Mbit)" value={rate} onChange={setRate} disabled={!canWrite} />
      <TextField label="Ceiling (Mbit)" value={ceil} onChange={setCeil} disabled={!canWrite} placeholder="optional" />
      <TextField label="Priority" value={priority} onChange={setPriority} disabled={!canWrite} placeholder="optional" />
      <Button type="submit" size="sm" disabled={!canWrite || !changed} title={writeDisabledReason}>
        Stage edit
      </Button>
      <Button type="button" size="sm" variant="secondary" onClick={onDone}>
        Cancel
      </Button>
    </form>
  );
}

/** "" and anything non-numeric both mean "leave this field out" — never 0,
 * which is a real (and invalid) rate the server would reject. */
function parseOptionalInt(raw: string): number | undefined {
  const trimmed = raw.trim();
  if (trimmed === "") return undefined;
  const n = Number.parseInt(trimmed, 10);
  return Number.isFinite(n) ? n : undefined;
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1 text-xs">
      {label}
      {children}
    </label>
  );
}

function TextField({
  label,
  value,
  onChange,
  disabled,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  placeholder?: string;
}) {
  return (
    <Field label={label}>
      <input
        type="text"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        disabled={disabled}
        placeholder={placeholder}
        className="w-32 rounded border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
      />
    </Field>
  );
}
