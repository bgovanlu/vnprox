// Pure Op-construction helpers for QoS shaping, the same seam
// changesets/opBuilders.ts provides for the entity editors: the wire shape
// (internal/change/params_qos.go) is honored in exactly one place, and no
// component hand-builds an Op.
//
// **This is the whole QoS write path.** There is no QoS write route —
// internal/api/qos.go serves `GET /qos/shapes` and nothing else — so a shape
// is created, edited or deleted only by staging one of these three ops into
// a changeset and taking it through the ordinary stage → validate → diff →
// apply → confirm flow. A test in qosOps.test.ts asserts that property
// against the shipped api/qos.ts source rather than trusting this comment.
//
// Framework-free (no React import) so it is directly Vitest-able.
import type { Op, QosShapeCreateParams, QosShapeUpdateParams } from "../api/types";

/** The form an operator fills in for one shape. `rateMbit` is required and
 * positive; `ceilMbit`, when set, must be >= `rateMbit` — both are enforced
 * server-side by internal/change/validate_schema.go, and this module does
 * not duplicate that validation, it just declines to send an empty field. */
export interface QosShapeFormValues {
  bridge: string;
  rateMbit: number;
  ceilMbit?: number;
  matchCidr?: string;
  matchVlan?: number;
  priority?: number;
}

/** A `qos-shape:<node>:<id>` Ref (inventory.KindQosShape). The shape has no
 * interfaces(5) stanza of its own, so the id is caller-chosen — the same
 * "caller-chosen id, no live-polled entity" shape nat-rule/static-route
 * Refs have. */
export function qosShapeRef(node: string, id: string): string {
  return `qos-shape:${node}:${id}`;
}

export function buildQosShapeCreateOp(node: string, id: string, form: QosShapeFormValues): Op {
  const params: QosShapeCreateParams = {
    bridge: form.bridge,
    rateMbit: form.rateMbit,
    ceilMbit: form.ceilMbit,
    matchCidr: form.matchCidr === "" ? undefined : form.matchCidr,
    matchVlan: form.matchVlan,
    priority: form.priority,
  };
  return { op: "qos.shape.create", target: qosShapeRef(node, id), params };
}

/** A partial `qos.shape.update` carrying only the fields that differ from
 * `initial` — absent means unchanged (params_qos.go's pointer-field
 * convention). Re-scoping which traffic a shape selects is deliberately a
 * delete-and-recreate rather than an in-place edit, so a caller changing
 * `matchCidr`/`matchVlan` should build two visible ops instead; this
 * function still emits them when they differ, because refusing silently
 * would be worse than letting the server's own validation speak. */
export function buildQosShapeUpdateOp(
  node: string,
  id: string,
  initial: QosShapeFormValues,
  form: QosShapeFormValues,
): Op {
  const params: QosShapeUpdateParams = {};
  if (form.bridge !== initial.bridge) params.bridge = form.bridge;
  if (form.rateMbit !== initial.rateMbit) params.rateMbit = form.rateMbit;
  if (form.ceilMbit !== initial.ceilMbit) params.ceilMbit = form.ceilMbit;
  if (form.matchCidr !== initial.matchCidr) params.matchCidr = form.matchCidr ?? "";
  if (form.matchVlan !== initial.matchVlan) params.matchVlan = form.matchVlan;
  if (form.priority !== initial.priority) params.priority = form.priority;
  return { op: "qos.shape.update", target: qosShapeRef(node, id), params };
}

/** `qos.shape.delete` carries no params — the target Ref is the whole
 * input (internal/qos.RenderTCTeardown re-derives the classid from it). */
export function buildQosShapeDeleteOp(node: string, id: string): Op {
  return { op: "qos.shape.delete", target: qosShapeRef(node, id), params: {} };
}

/** True when `form` differs from `initial` in any field an update op would
 * carry — the "nothing to stage" guard, so a no-op edit does not create a
 * changeset with an empty-params update in it. */
export function qosShapeFormChanged(initial: QosShapeFormValues, form: QosShapeFormValues): boolean {
  return (
    form.bridge !== initial.bridge ||
    form.rateMbit !== initial.rateMbit ||
    form.ceilMbit !== initial.ceilMbit ||
    form.matchCidr !== initial.matchCidr ||
    form.matchVlan !== initial.matchVlan ||
    form.priority !== initial.priority
  );
}
