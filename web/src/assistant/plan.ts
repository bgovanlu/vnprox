// SPDX-License-Identifier: Apache-2.0

// T-2808: which of the mirrored MCP read tools a question needs.
//
// Deliberately a small, deterministic, testable rule set rather than a
// model-driven tool loop. Two reasons, both about the card's boundaries:
// a rule set cannot be talked into calling a tool (the model never chooses
// what runs), and a turn's tool calls stay bounded and inspectable, so the
// panel can show exactly which surfaces were read.
//
// The two argument-taking tools only plan when the question itself supplies
// their arguments — a ref for the diagnosis ladder, two IPs for the path
// simulator. Nothing is invented on the model's behalf.
import type { AssistantToolName, PlannedCall } from "./tools";

/** kind:node:id — the ref vocabulary every vnprox surface uses. The middle
 * segment may be empty (`sdn-subnet::10.0.0.0/24`). */
const REF_PATTERN = /\b[a-z][a-z0-9-]*:[a-zA-Z0-9._-]*:[^\s,;]+/g;

/** A dotted-quad. Deliberately IPv4-only: a v6 literal inside prose is not
 * reliably separable from a ref, and a wrong pair of endpoints is worse
 * than not planning the simulator at all. */
const IPV4_PATTERN = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g;

const FLOW_WORDS = /\b(flow|flows|traffic|talking|talks|internet|egress|bandwidth|port|ports)\b/i;
const IPAM_WORDS = /\b(subnet|subnets|ipam|address|addresses|cidr|utilis|utiliz|dhcp|free ip)\w*/i;
const DIAGNOSE_WORDS = /\b(diagnose|diagnosis|broken|failing|down|unreachable|why)\b/i;
const REACH_WORDS = /\b(reach|reaches|reachable|path|route|get to|connect)\b/i;

function matches(question: string, pattern: RegExp): string[] {
  return [...question.matchAll(pattern)].map((m) => m[0]);
}

/**
 * Plans the tool calls for one question, filtered to the tools this caller
 * is permitted (AC3: a surface the caller cannot reach directly is never
 * planned, and — should the filter ever be wrong — the server still refuses
 * it; see runTool's 403 handling).
 *
 * topology + findings are always planned: "what does the network look like"
 * and "what is currently wrong with it" are the context nearly every
 * question needs, and they are the two cheapest reads.
 */
export function planTools(question: string, permitted: readonly AssistantToolName[]): PlannedCall[] {
  const allow = new Set(permitted);
  const planned: PlannedCall[] = [];
  const add = (call: PlannedCall): void => {
    if (allow.has(call.tool)) {
      planned.push(call);
    }
  };

  add({ tool: "topology.get" });
  add({ tool: "findings.list" });

  if (FLOW_WORDS.test(question)) {
    add({ tool: "flows.query" });
  }
  if (IPAM_WORDS.test(question)) {
    add({ tool: "ipam.subnets.list" });
  }

  const refs = matches(question, REF_PATTERN);
  const firstRef = refs[0];
  if (firstRef !== undefined && DIAGNOSE_WORDS.test(question)) {
    add({ tool: "diagnose.run", targetRef: firstRef });
  }

  const ips = matches(question, IPV4_PATTERN);
  const src = ips[0];
  const dst = ips[1];
  if (src !== undefined && dst !== undefined && REACH_WORDS.test(question)) {
    add({ tool: "simulate.path", src, dst });
  }

  return planned;
}
