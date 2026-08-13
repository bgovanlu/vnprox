// T-2808: the in-app assistant's tool catalogue.
//
// This is a CLIENT-SIDE MIRROR of the MCP read tools (internal/mcp/
// registry.go's frozen allowlist), not a second surface. Each entry names
// the same tool the MCP server exposes, and runs it by calling the same
// `/api/v1` route that tool wraps — through the app's ordinary api layer,
// with the caller's own session cookie. That is what the card means by "no
// new backend capability, no new data path, no separate authorisation
// model": the assistant reaches exactly what the person driving it could
// already reach by clicking, and the server's capability gate is the only
// authorisation there is.
//
// Two structural checks keep the mirror honest rather than aspirational:
//
//   - internal/mcp's TestAssistantCatalogIsASubsetOfTheFrozenAllowlist
//     parses the ASSISTANT_TOOLS table below out of this file and fails if a
//     name is not in the Go allowlist, or names a mutating verb.
//   - boundary.test.ts asserts no module under web/src/assistant imports an
//     apply/confirm/rollback API call.
//
// Nothing here writes. The one write the panel can perform (staging a draft,
// AC4) lives in proposals.ts and goes through the ordinary POST /changesets
// path every editor in the app already uses.
import { fetchTopology } from "../api/topology";
import { fetchFindings } from "../api/findings";
import { fetchFlows } from "../api/flows";
import { fetchIpamSubnets } from "../api/ipam";
import { simulatePath } from "../api/simulate";
import { postDiagnose } from "../api/diagnose";
import { ApiError } from "../api/client";
import type { Capabilities } from "../api/types";

/** The tool names, spelled exactly as internal/mcp/registry.go spells them.
 * The Go-side test above pins this equality; do not "tidy" a name here. */
export type AssistantToolName =
  | "topology.get"
  | "findings.list"
  | "flows.query"
  | "ipam.subnets.list"
  | "simulate.path"
  | "diagnose.run";

/** One planned tool invocation. A discriminated union rather than a bag of
 * optional fields, so a call that needs arguments cannot be constructed
 * without them (plan.ts is the only producer). */
export type PlannedCall =
  | { tool: "topology.get" }
  | { tool: "findings.list" }
  | { tool: "flows.query" }
  | { tool: "ipam.subnets.list" }
  | { tool: "simulate.path"; src: string; dst: string }
  | { tool: "diagnose.run"; targetRef: string };

/** Something a citation may point at: an entity or a tool result that
 * demonstrably came back from this turn's tool calls. `href` is the screen
 * that shows it, so a citation is also a way into the product. */
export interface CitableEntity {
  ref: string;
  label: string;
  href: string;
}

/** The outcome of one tool call. `refused` is the case that matters for
 * AC3: the server said no (403), so this surface contributed NOTHING —
 * `entities` is empty, and therefore no citation naming this tool can ever
 * resolve (see citations.ts). */
export interface ToolRun {
  tool: AssistantToolName;
  status: "ok" | "refused" | "failed";
  /** Human explanation for a refused/failed run. Never carries the user's
   * question (AC6) — it is an API error, not an echo of the prompt. */
  note?: string;
  entities: CitableEntity[];
  /** The compact projection handed to the model. Deliberately a summary,
   * not the raw response: a model does not need every field, and a smaller
   * context is a smaller thing to send anywhere. */
  summary: unknown;
}

export interface AssistantToolSpec {
  name: AssistantToolName;
  /** The capability the wrapped route is gated by, per internal/api
   * (topology/findings/flows/simulate/diagnose: netRead; ipam: sdnRead —
   * capIPAMRead is capSDNRead). The client checks it to avoid making a call
   * that can only 403; the SERVER's check is the authorisation. */
  requiredCap: keyof Capabilities;
  /** The MCP tool's own description, kept short. */
  description: string;
}

export const ASSISTANT_TOOLS: readonly AssistantToolSpec[] = [
  {
    name: "topology.get",
    requiredCap: "netRead",
    description: "The current cluster network topology projection (GET /topology).",
  },
  {
    name: "findings.list",
    requiredCap: "netRead",
    description: "The open findings stream (GET /findings).",
  },
  {
    name: "flows.query",
    requiredCap: "netRead",
    description: "Observed network flows (GET /flows).",
  },
  {
    name: "ipam.subnets.list",
    requiredCap: "sdnRead",
    description: "Known subnets with utilisation and conflicts (GET /ipam/subnets).",
  },
  {
    name: "simulate.path",
    requiredCap: "netRead",
    description: "Static firewall+routing path simulation (POST /simulate/path).",
  },
  {
    name: "diagnose.run",
    requiredCap: "netRead",
    description: "The guided diagnosis ladder, advisory only (POST /diagnose).",
  },
];

export function toolSpec(name: AssistantToolName): AssistantToolSpec {
  const found = ASSISTANT_TOOLS.find((t) => t.name === name);
  if (!found) {
    // Unreachable: AssistantToolName and ASSISTANT_TOOLS are edited
    // together, and the Go-side catalogue test fails if they drift.
    throw new Error(`assistant: no spec for tool ${name}`);
  }
  return found;
}

/** True when the session holds `cap` on any node. Matches the convention
 * settings/SettingsPage.tsx already uses for rendering capability chips —
 * per-node caps, "anywhere" is the useful question for a cluster-wide read.
 * A false answer here only skips a call that would 403 anyway. */
export function hasCapAnywhere(caps: Record<string, Capabilities>, cap: keyof Capabilities): boolean {
  return Object.values(caps).some((c) => c[cap]);
}

/** The tools this caller may run. Used to plan a turn and, in the panel, to
 * say which surfaces were unavailable — never to widen anything. */
export function permittedTools(caps: Record<string, Capabilities>): AssistantToolName[] {
  return ASSISTANT_TOOLS.filter((t) => hasCapAnywhere(caps, t.requiredCap)).map((t) => t.name);
}

const MAX_ENTITIES = 60;

function refused(tool: AssistantToolName, note: string): ToolRun {
  return { tool, status: "refused", note, entities: [], summary: undefined };
}

function failed(tool: AssistantToolName, note: string): ToolRun {
  return { tool, status: "failed", note, entities: [], summary: undefined };
}

/** Turns a thrown api error into a run outcome. A 401/403 is the caller's
 * own authorisation speaking and is reported as `refused`; anything else is
 * an ordinary failure. Either way `entities` stays empty, so nothing from a
 * surface the caller could not reach can be cited. */
function fromError(tool: AssistantToolName, err: unknown): ToolRun {
  if (err instanceof ApiError && (err.status === 403 || err.status === 401)) {
    return refused(tool, `the server refused this surface for your session (${String(err.status)})`);
  }
  const message = err instanceof Error ? err.message : String(err);
  return failed(tool, message);
}

/** Runs one planned call against the live API. Every branch returns a
 * ToolRun; nothing throws, because one unreachable surface must not lose a
 * turn's other evidence. */
export async function runTool(call: PlannedCall): Promise<ToolRun> {
  try {
    switch (call.tool) {
      case "topology.get": {
        const res = await fetchTopology({});
        const entities = res.nodes.slice(0, MAX_ENTITIES).map((n) => ({
          ref: n.id,
          label: `${n.label} (${n.kind})`,
          href: "/topology",
        }));
        return {
          tool: call.tool,
          status: "ok",
          entities,
          summary: {
            nodeCount: res.nodes.length,
            edgeCount: res.edges.length,
            nodes: res.nodes.slice(0, MAX_ENTITIES).map((n) => ({
              ref: n.id,
              kind: n.kind,
              label: n.label,
              node: n.nodeGroup,
              status: n.status,
            })),
          },
        };
      }
      case "findings.list": {
        const items = await fetchFindings({});
        const entities = items.slice(0, MAX_ENTITIES).map((f) => ({
          ref: f.id,
          label: `${f.check} — ${f.severity}`,
          href: "/tools",
        }));
        return {
          tool: call.tool,
          status: "ok",
          entities,
          summary: {
            count: items.length,
            findings: items.slice(0, MAX_ENTITIES).map((f) => ({
              ref: f.id,
              source: f.source,
              check: f.check,
              severity: f.severity,
              detail: f.detail,
              nodes: f.nodes,
              fixable: f.fixable,
            })),
          },
        };
      }
      case "flows.query": {
        const page = await fetchFlows({ limit: 100 });
        const seen = new Set<string>();
        const entities: CitableEntity[] = [];
        for (const flow of page.items) {
          for (const ref of [flow.srcRef, flow.dstRef]) {
            if (ref === undefined || ref === "" || seen.has(ref) || entities.length >= MAX_ENTITIES) {
              continue;
            }
            seen.add(ref);
            entities.push({ ref, label: ref, href: `/flows?guest=${encodeURIComponent(ref)}` });
          }
        }
        return {
          tool: call.tool,
          status: "ok",
          entities,
          summary: {
            count: page.items.length,
            partial: page.partial === true,
            flows: page.items.slice(0, MAX_ENTITIES).map((f) => ({
              srcIp: f.srcIp,
              dstIp: f.dstIp,
              dstPort: f.dstPort,
              proto: f.proto,
              bytes: f.bytes,
              srcRef: f.srcRef,
              dstRef: f.dstRef,
            })),
          },
        };
      }
      case "ipam.subnets.list": {
        const res = await fetchIpamSubnets();
        const entities = res.items.slice(0, MAX_ENTITIES).map((s) => ({
          ref: s.cidr,
          label: `${s.cidr} (${String(Math.round(s.utilization * 100))}% used)`,
          href: "/ipam",
        }));
        return {
          tool: call.tool,
          status: "ok",
          entities,
          summary: {
            count: res.items.length,
            subnets: res.items.slice(0, MAX_ENTITIES).map((s) => ({
              ref: s.cidr,
              zone: s.zone,
              vnet: s.vnet,
              source: s.source,
              total: s.total,
              allocated: s.allocated,
              conflicts: s.conflicts,
            })),
          },
        };
      }
      case "simulate.path": {
        const res = await simulatePath({ src: { kind: "ip", ip: call.src }, dst: { kind: "ip", ip: call.dst } });
        const ref = `simulate:${call.src}->${call.dst}`;
        return {
          tool: call.tool,
          status: "ok",
          entities: [{ ref, label: `${call.src} → ${call.dst}: ${res.verdict}`, href: "/tools" }],
          summary: {
            ref,
            verdict: res.verdict,
            hops: res.hops.length,
            blockingRule: res.blockingRule?.action,
            caveats: res.caveats.map((c) => c.code),
          },
        };
      }
      case "diagnose.run": {
        // escalateToCapture is passed explicitly false, mirroring the MCP
        // tool's own contract ("never escalates to packet capture over
        // MCP"): the assistant runs the advisory ladder, never a capture.
        const res = await postDiagnose({ targetRef: call.targetRef, escalateToCapture: false });
        return {
          tool: call.tool,
          status: "ok",
          entities: [
            {
              ref: res.target,
              label: `diagnosis of ${res.target}: ${res.verdict.summary}`,
              href: `/diagnose?ref=${encodeURIComponent(res.target)}`,
            },
          ],
          summary: {
            ref: res.target,
            confidence: res.verdict.confidence,
            summary: res.verdict.summary,
            linkedFindingIds: res.verdict.linkedFindingIds,
            steps: res.steps.map((s) => ({ name: s.name, status: s.status, summary: s.summary })),
          },
        };
      }
    }
  } catch (err) {
    return fromError(call.tool, err);
  }
}
