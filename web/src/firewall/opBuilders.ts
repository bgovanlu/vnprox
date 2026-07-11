// Pure Op-construction helpers for T-502's fw.* op family, mirroring
// web/src/changesets/opBuilders.ts's existing pattern for bridge/bond/vlan:
// every editor funnels through one of these instead of hand-building an
// `Op` object, so the wire shape (internal/change/params_fw.go) is honored
// in exactly one place. Framework-free (no React import) so it's directly
// Vitest-able.
import type {
  FwAliasCreateParams,
  FwAliasUpdateParams,
  FwGroupCreateParams,
  FwGroupUpdateParams,
  FwIpsetCreateParams,
  FwIpsetUpdateParams,
  FwOptionsUpdateParams,
  FwRuleCreateParams,
  FwRuleFields,
  FwRuleSpec,
  FwRuleUpdateParams,
  Op,
  RuleView,
} from "../api/types";

export interface RuleFormValues {
  direction: string;
  action: string;
  proto: string;
  source: string;
  dest: string;
  sport: string;
  dport: string;
  iface: string;
  macro: string;
  log: string;
  comment: string;
  enabled: boolean;
}

/** Extracts a rule's content-identity fields (internal/change.FwRuleFields'
 * wire shape) from a fetched RuleView — the "what I observed at draft
 * time" value fw.rule.move's Expect and fw.rule.update's merge-base both
 * need. */
export function ruleFieldsFrom(rule: RuleView): FwRuleFields {
  return {
    direction: rule.direction,
    action: rule.action,
    proto: rule.proto,
    source: rule.source,
    dest: rule.dest,
    sport: rule.sport,
    dport: rule.dport,
    iface: rule.iface,
    macro: rule.macro,
    log: rule.log,
    comment: rule.comment,
    enabled: rule.enabled,
  };
}

export function buildFwRuleCreateOp(target: string, pos: number, form: RuleFormValues): Op {
  const params: FwRuleCreateParams = {
    direction: form.direction,
    action: form.action,
    proto: form.proto || undefined,
    source: form.source || undefined,
    dest: form.dest || undefined,
    sport: form.sport || undefined,
    dport: form.dport || undefined,
    iface: form.iface || undefined,
    macro: form.macro || undefined,
    log: form.log || undefined,
    comment: form.comment || undefined,
    pos,
    enabled: form.enabled,
  };
  return { op: "fw.rule.create", target, params };
}

/** A partial fw.rule.update carrying only the fields that differ from
 * `initial` (the same "diff against initial form state" convention
 * web/src/changesets/opBuilders.ts's bridge/bond update builders use). */
export function buildFwRuleUpdateOp(target: string, pos: number, initial: RuleFormValues, form: RuleFormValues): Op {
  const params: FwRuleUpdateParams = { pos };
  if (form.direction !== initial.direction) params.direction = form.direction;
  if (form.action !== initial.action) params.action = form.action;
  if (form.proto !== initial.proto) params.proto = form.proto;
  if (form.source !== initial.source) params.source = form.source;
  if (form.dest !== initial.dest) params.dest = form.dest;
  if (form.sport !== initial.sport) params.sport = form.sport;
  if (form.dport !== initial.dport) params.dport = form.dport;
  if (form.iface !== initial.iface) params.iface = form.iface;
  if (form.macro !== initial.macro) params.macro = form.macro;
  if (form.log !== initial.log) params.log = form.log;
  if (form.comment !== initial.comment) params.comment = form.comment;
  if (form.enabled !== initial.enabled) params.enabled = form.enabled;
  return { op: "fw.rule.update", target, params };
}

/** Inline enable/disable toggle: a minimal fw.rule.update touching only
 * `enabled`. */
export function buildFwRuleToggleOp(target: string, pos: number, enabled: boolean): Op {
  return { op: "fw.rule.update", target, params: { pos, enabled } satisfies FwRuleUpdateParams };
}

export function buildFwRuleDeleteOp(target: string, pos: number): Op {
  return { op: "fw.rule.delete", target, params: { pos } };
}

/** Builds the fw.rule.move op for a drag-to-reorder gesture: `rule` is the
 * dragged rule as observed at drag start (its content becomes Expect, the
 * apply-time position-race guard — acceptance criterion 3), `toPos` is its
 * desired final position in the ruleset. */
export function buildFwRuleMoveOp(target: string, rule: RuleView, toPos: number): Op {
  return {
    op: "fw.rule.move",
    target,
    params: { fromPos: rule.pos, toPos, expect: ruleFieldsFrom(rule) },
  };
}

export function buildFwOptionsUpdateOp(target: string, params: FwOptionsUpdateParams): Op {
  return { op: "fw.options.update", target, params };
}

export function buildFwAliasCreateOp(target: string, name: string, cidr: string, comment?: string): Op {
  const params: FwAliasCreateParams = { name, cidr, comment: comment ?? undefined };
  return { op: "fw.alias.create", target, params };
}

export function buildFwAliasUpdateOp(target: string, name: string, patch: { cidr?: string; comment?: string }): Op {
  const params: FwAliasUpdateParams = { name, ...patch };
  return { op: "fw.alias.update", target, params };
}

export function buildFwAliasDeleteOp(target: string, name: string): Op {
  return { op: "fw.alias.delete", target, params: { name } };
}

export function buildFwIpsetCreateOp(target: string, name: string, cidrs: string[], comment?: string): Op {
  const params: FwIpsetCreateParams = { name, cidrs, comment: comment ?? undefined };
  return { op: "fw.ipset.create", target, params };
}

export function buildFwIpsetUpdateOp(target: string, name: string, patch: { cidrs?: string[]; comment?: string }): Op {
  const params: FwIpsetUpdateParams = { name, ...patch };
  return { op: "fw.ipset.update", target, params };
}

export function buildFwIpsetDeleteOp(target: string, name: string): Op {
  return { op: "fw.ipset.delete", target, params: { name } };
}

export function buildFwGroupCreateOp(target: string, name: string, rules: FwRuleSpec[], comment?: string): Op {
  const params: FwGroupCreateParams = { name, comment: comment ?? undefined, rules };
  return { op: "fw.group.create", target, params };
}

export function buildFwGroupUpdateOp(target: string, name: string, patch: { rules?: FwRuleSpec[]; comment?: string }): Op {
  const params: FwGroupUpdateParams = { name, ...patch };
  return { op: "fw.group.update", target, params };
}

export function buildFwGroupDeleteOp(target: string, name: string): Op {
  return { op: "fw.group.delete", target, params: { name } };
}
