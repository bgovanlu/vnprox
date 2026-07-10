// Maps a validation Finding's stable code (internal/change/validate_codes.go
// — `<class>.<check>` dotted identifiers) to the editor form field that
// originated it, so an error can render on that field itself and not just as
// a drawer-line badge (T-207 acceptance criterion 2: "Validation errors
// render on the drawer line *and* the originating form field").
// Framework-free and directly Vitest-able.
//
// The field keys here are this frontend's own logical field names (the ones
// EditorDialog's <Field> instances register under), not wire params — one
// code can only ever point at one field per editor, which holds for every
// code T-202 emits today. Codes with no single originating field (e.g.
// referential.already_exists — the entity name itself) map to undefined and
// render in the editor's general error area instead.
import type { Finding, Op } from "../api/types";

const CODE_TO_FIELD: Record<string, string> = {
  // schema
  "schema.mtu_out_of_range": "mtu",
  "schema.vid_out_of_range": "vid",
  "schema.vid_range_invalid": "vids",
  "schema.bond_mode_invalid": "mode",
  "schema.lacp_rate_invalid": "lacpRate",
  "schema.xmit_hash_policy_invalid": "xmitHashPolicy",
  "schema.miimon_invalid": "miimon",
  "schema.duplicate_slave": "slaves",
  "schema.duplicate_port": "ports",
  "schema.cidr_invalid": "addresses",
  "schema.ip_invalid": "gateway",
  "schema.rate_invalid": "rateMbps",
  // referential
  "referential.parent_not_found": "parent",
  "referential.slave_not_found": "slaves",
  "referential.port_not_found": "ports",
  "referential.port_not_attached": "ports",
  "referential.duplicate_enslavement": "slaves",
  "referential.bridge_or_vnet_not_found": "bridgeOrVnet",
  "referential.vid_overlap": "vids",
  "referential.address_overlap": "addresses",
  "referential.address_out_of_subnet": "addresses",
};

/** The logical form-field key a finding code originates from, or undefined
 * when the finding isn't attributable to any single field. */
export function fieldForFindingCode(code: string): string | undefined {
  return CODE_TO_FIELD[code];
}

export interface EditorFindings {
  /** Error messages keyed by logical field name, for inline field rendering. */
  byField: Record<string, string[]>;
  /** Error messages with no single originating field — the editor's general
   * error area renders these. */
  general: string[];
}

/** Splits the error-severity findings for one target ref into per-field and
 * general buckets. Warning/info findings are excluded — they don't block an
 * editor submit (the review screen's warnings checkbox owns those). */
export function editorFindingsFor(findings: Finding[], targetRef: string): EditorFindings {
  const byField: Record<string, string[]> = {};
  const general: string[] = [];
  for (const f of findings) {
    if (f.severity !== "error" || f.ref !== targetRef) continue;
    const field = fieldForFindingCode(f.code);
    if (field === undefined) {
      general.push(f.message);
    } else {
      (byField[field] ??= []).push(f.message);
    }
  }
  return { byField, general };
}

/** True when the split contains any error at all — the editor stays open. */
export function hasEditorErrors(split: EditorFindings): boolean {
  return split.general.length > 0 || Object.keys(split.byField).length > 0;
}

/** Applies a finding's machine-applicable `fix` patch to an ops list (the
 * one-click apply — T-207 acceptance criterion 2). A fix op replaces the
 * changeset op sharing its exact (op type, target) pair — internal/change/
 * validate_fix.go: "Every fix is a one-op patch sharing the offending op's
 * exact Type/Target" — so matching on that pair is the wire contract, not a
 * heuristic. Ops without a matching fix pass through unchanged. */
export function applyFixToOps(ops: Op[], fix: Op[]): Op[] {
  return ops.map((op) => fix.find((f) => f.op === op.op && f.target === op.target) ?? op);
}
