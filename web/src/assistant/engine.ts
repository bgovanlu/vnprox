// SPDX-License-Identifier: Apache-2.0

// T-2808: one turn of the assistant.
//
// The order of operations is the whole design:
//
//   1. No backend configured -> return immediately. The transport is not
//      called, not constructed, not reached (AC1). Note that this happens
//      BEFORE any tool runs too: with nothing to answer with, reading the
//      cluster would be pointless work.
//   2. Plan tool calls from the question, filtered to the caller's own
//      capabilities (AC3), and run them through the ordinary api layer with
//      the caller's session.
//   3. Send question + tool evidence to the configured backend.
//   4. Refuse to render an answer that cites nothing real (AC2, citations.ts).
//
// What is deliberately absent: any path where the model chooses a tool, any
// path where it applies anything, and any path where the prompt or the
// answer reaches vnproxd (AC6).
import type { Capabilities } from "../api/types";
import { permittedTools, runTool, type ToolRun } from "./tools";
import { planTools } from "./plan";
import { evaluateReply, type AssistantResult } from "./citations";
import type { ModelBackend, ModelTransport } from "./backend";

export interface AskOptions {
  question: string;
  /** GET /auth/me's per-node capability map — the caller's own. */
  caps: Record<string, Capabilities>;
  /** undefined means "no model backend configured", the default state. */
  backend: ModelBackend | undefined;
  /** Built from `backend` by the caller (createHttpModelTransport). Passing
   * it in keeps this function free of transport construction, so a test can
   * hand it one that fails if it is ever called. */
  transport: ModelTransport;
  /** Seam for tests; production passes the real one implicitly. */
  run?: typeof runTool;
}

export async function ask(options: AskOptions): Promise<AssistantResult> {
  if (options.backend === undefined) {
    // Nothing is sent anywhere. The panel renders this as a plain statement
    // that no backend is configured — not an error, and not a hint that
    // something went wrong.
    return { status: "no-backend" };
  }

  const runTool_ = options.run ?? runTool;
  const calls = planTools(options.question, permittedTools(options.caps));
  const runs: ToolRun[] = [];
  for (const call of calls) {
    runs.push(await runTool_(call));
  }

  const context = runs
    .filter((r) => r.status === "ok")
    .map((r) => ({ tool: r.tool, summary: r.summary }));
  const citableRefs = runs
    .filter((r) => r.status === "ok")
    .flatMap((r) => r.entities.map((e) => ({ tool: r.tool, ref: e.ref, label: e.label })));

  if (citableRefs.length === 0) {
    // Every surface this question needed was refused, failed, or empty.
    // There is nothing an answer could cite, so there is no point sending
    // the question anywhere — and, in the AC3 case, sending it would mean
    // asking a model about data the caller may not see.
    return { status: "withheld", reason: "no-resolving-citation", unresolved: [], runs };
  }

  let reply: string;
  try {
    reply = await options.transport({ question: options.question, context, citableRefs });
  } catch (err) {
    // The message is the transport's own (a status code, a network error).
    // It never contains the question: see backend.ts's createHttpModelTransport.
    return { status: "error", message: err instanceof Error ? err.message : String(err), runs };
  }

  return evaluateReply(reply, runs);
}
