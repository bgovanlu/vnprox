// Pure, framework-free op-summary rendering for the drawer and the review
// screen's Summary tab — the client-side mirror of internal/change/ifaces/
// summary.go's Summarize(), extended to cover every op family T-207's
// editors can produce (that Go file only covers the node-file ops T-204
// diffs; guest.nic.update and sdn.apply have no diff-tab representation at
// all, so the drawer is the only place their summary text renders).
// Kept in its own module (no React import) so it's directly Vitest-able
// without mounting anything (T-207 acceptance criterion 5).
import type { Op } from "../api/types";

/** A Ref string's node segment, per inventory.Ref's "kind:node:id" encoding
 * (ParseRef splits on only the first two ':' — mirrors topology/expand.ts's
 * identical helper, kept local here so this module has zero imports from
 * the topology feature). */
export function refNode(ref: string): string {
  const first = ref.indexOf(":");
  if (first === -1) return "";
  const second = ref.indexOf(":", first + 1);
  if (second === -1) return "";
  return ref.slice(first + 1, second);
}

/** A Ref string's id segment (everything after the second ':'). */
export function refId(ref: string): string {
  const first = ref.indexOf(":");
  if (first === -1) return ref;
  const second = ref.indexOf(":", first + 1);
  if (second === -1) return "";
  return ref.slice(second + 1);
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null;
}

function str(params: unknown, key: string): string | undefined {
  if (!isRecord(params)) return undefined;
  const v = params[key];
  return typeof v === "string" ? v : undefined;
}

function num(params: unknown, key: string): number | undefined {
  if (!isRecord(params)) return undefined;
  const v = params[key];
  return typeof v === "number" ? v : undefined;
}

function bool(params: unknown, key: string): boolean | undefined {
  if (!isRecord(params)) return undefined;
  const v = params[key];
  return typeof v === "boolean" ? v : undefined;
}

function arr(params: unknown, key: string): unknown[] | undefined {
  if (!isRecord(params)) return undefined;
  const v = params[key];
  return Array.isArray(v) ? v : undefined;
}

function strArr(params: unknown, key: string): string[] | undefined {
  const a = arr(params, key);
  if (!a) return undefined;
  return a.every((x) => typeof x === "string") ? a : undefined;
}

/** One human-readable line per op, for the drawer's op list. Mirrors
 * internal/change/ifaces/summary.go's field-list style for update ops
 * ("no changes" when nothing is set) so the frontend and the backend Plan/
 * Diff tabs never disagree about what an op "means" in prose. */
export function summarizeOp(op: Op): string {
  const id = op.target ? refId(op.target) : undefined;
  switch (op.op) {
    case "iface.update": {
      const fields = updateFieldList(op.params);
      return `Update interface ${id ?? "?"} (${fields})`;
    }
    case "bond.create": {
      const mode = str(op.params, "mode") ?? "-";
      const slaves = strArr(op.params, "slaves") ?? [];
      return `Create bond ${id ?? "?"} (${mode}) from ${slaves.join(", ") || "no slaves"}`;
    }
    case "bond.update":
      return `Update bond ${id ?? "?"} (${updateFieldList(op.params)})`;
    case "bond.delete":
      return `Delete bond ${id ?? "?"}`;
    case "bridge.create": {
      const ports = strArr(op.params, "ports") ?? [];
      const kind = id?.startsWith("vmbr") === false ? "OVS bridge" : "bridge";
      return `Create ${kind} ${id ?? "?"} with ports ${ports.join(", ") || "none"}`;
    }
    case "bridge.update":
      return `Update bridge ${id ?? "?"} (${updateFieldList(op.params)})`;
    case "bridge.delete":
      return `Delete bridge ${id ?? "?"}`;
    case "bridge.port.add":
      return `Add port ${str(op.params, "port") ?? "?"} to bridge ${id ?? "?"}`;
    case "bridge.port.remove":
      return `Remove port ${str(op.params, "port") ?? "?"} from bridge ${id ?? "?"}`;
    case "vlan.create": {
      const parent = str(op.params, "parent") ?? "?";
      const vid = num(op.params, "vid");
      return `Create VLAN ${id ?? "?"} (vid ${vid === undefined ? "?" : String(vid)} on ${parent})`;
    }
    case "vlan.update":
      return `Update VLAN ${id ?? "?"} (${updateFieldList(op.params)})`;
    case "vlan.delete":
      return `Delete VLAN ${id ?? "?"}`;
    case "guest.nic.update": {
      const parts: string[] = [];
      const target = str(op.params, "bridgeOrVnet");
      if (target !== undefined) parts.push(`reattach to ${target}`);
      const vid = num(op.params, "vid");
      if (vid !== undefined) parts.push(`vid=${String(vid)}`);
      const rate = num(op.params, "rateMbps");
      if (rate !== undefined) parts.push(`rate=${String(rate)}Mbps`);
      const fw = bool(op.params, "firewall");
      if (fw !== undefined) parts.push(`firewall=${String(fw)}`);
      const down = bool(op.params, "linkDown");
      if (down !== undefined) parts.push(down ? "disconnect" : "connect");
      return `Update guest NIC ${id ?? "?"} (${parts.length > 0 ? parts.join(", ") : "no changes"})`;
    }
    case "sdn.apply":
      return "Apply pending SDN configuration (cluster-wide)";
    default:
      return `${op.op} ${id ?? ""}`.trim();
  }
}

function updateFieldList(params: unknown): string {
  const fields: string[] = [];
  const mtu = num(params, "mtu");
  if (mtu !== undefined) fields.push(`mtu=${String(mtu)}`);
  const mode = str(params, "mode");
  if (mode !== undefined) fields.push(`mode=${mode}`);
  const slaves = strArr(params, "slaves");
  if (slaves !== undefined) fields.push("slaves");
  const addresses = strArr(params, "addresses");
  if (addresses !== undefined) fields.push("addresses");
  const gateway = str(params, "gateway");
  if (gateway !== undefined) fields.push("gateway");
  const autostart = bool(params, "autostart");
  if (autostart !== undefined) fields.push(`autostart=${String(autostart)}`);
  const comments = str(params, "comments") ?? str(params, "comment");
  if (comments !== undefined) fields.push("comments");
  const vlanAware = bool(params, "vlanAware");
  if (vlanAware !== undefined) fields.push(`vlanAware=${String(vlanAware)}`);
  const vids = arr(params, "vids");
  if (vids !== undefined) fields.push("vids");
  const stp = bool(params, "stp");
  if (stp !== undefined) fields.push(`stp=${String(stp)}`);
  const lacpRate = str(params, "lacpRate");
  if (lacpRate !== undefined) fields.push("lacpRate");
  const xmitHashPolicy = str(params, "xmitHashPolicy");
  if (xmitHashPolicy !== undefined) fields.push("xmitHashPolicy");
  const miimon = num(params, "miimon");
  if (miimon !== undefined) fields.push(`miimon=${String(miimon)}`);
  if (fields.length === 0) return "no changes";
  return fields.join(", ");
}

/** The op-family label the drawer groups/badges by (docs/features/
 * change-management.md §1: "human-readable summaries"). */
export function opKindLabel(op: Op): string {
  return op.op.split(".")[0] ?? op.op;
}
