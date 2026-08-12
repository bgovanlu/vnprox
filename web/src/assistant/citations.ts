// T-2808 AC2: an uncited answer is not rendered.
//
// The enforcement is a TYPE, not a convention. `AssistantResult` has two
// answer-bearing shapes and only one of them carries the answer text:
//
//   { status: "answered", answer, citations }   — citations is non-empty
//   { status: "withheld", ... }                 — has NO answer field
//
// So a renderer cannot show an uncited answer even by accident: after
// `evaluateReply` decides the answer is uncited, the text is gone from the
// value the panel is handed. There is no "render it anyway" branch to
// forget to write, and adding one would mean adding a field.
//
// A citation resolves only against THIS turn's tool results: the tool must
// have run, and the ref must be one that tool actually returned. That makes
// two failure modes indistinguishable and equally fatal, which is the point
// — a model that fabricates an entity and a model that cites a surface the
// caller could not reach (AC3: a refused run has no entities) both produce
// an unresolvable citation, and an answer with none left is withheld.
import type { AssistantToolName, CitableEntity, ToolRun } from "./tools";
import { parseProposals, type StagingProposal } from "./proposals";

/** A citation that resolved: it names a tool that ran and an entity that
 * tool returned, so it carries that entity's label and its deep link. */
export interface ResolvedCitation {
  tool: AssistantToolName;
  ref: string;
  label: string;
  href: string;
}

/** A citation the model asserted that nothing this turn supports. Kept (and
 * counted) rather than silently dropped: "the model cited three things and
 * one of them does not exist" is information an operator should see. */
export interface UnresolvedCitation {
  tool: string;
  ref: string;
}

export type AssistantResult =
  /** No model backend is configured. Nothing was sent anywhere. */
  | { status: "no-backend" }
  | {
      status: "answered";
      answer: string;
      /** Always non-empty — the constructor is the only producer, and it
       * returns `withheld` when this would be empty. */
      citations: ResolvedCitation[];
      unresolved: UnresolvedCitation[];
      proposals: StagingProposal[];
      runs: ToolRun[];
    }
  | {
      status: "withheld";
      reason: "no-resolving-citation" | "unparsable-reply";
      unresolved: UnresolvedCitation[];
      runs: ToolRun[];
    }
  | { status: "error"; message: string; runs: ToolRun[] };

interface RawReply {
  answer: string;
  citations: { tool: string; ref: string }[];
  proposals: StagingProposal[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/** Strips a ```json fence, which small models add even when told not to.
 * Anything else is passed through untouched. */
function unfence(text: string): string {
  const trimmed = text.trim();
  if (!trimmed.startsWith("```")) {
    return trimmed;
  }
  const withoutOpen = trimmed.replace(/^```[a-zA-Z]*\s*/, "");
  const close = withoutOpen.lastIndexOf("```");
  return (close === -1 ? withoutOpen : withoutOpen.slice(0, close)).trim();
}

/** Parses the reply contract. Returns undefined for anything that is not a
 * well-formed reply — including prose with no JSON at all, which is exactly
 * what an unconstrained model returns and exactly what must not be shown as
 * an answer. */
export function parseModelReply(text: string): RawReply | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(unfence(text));
  } catch {
    return undefined;
  }
  if (!isRecord(parsed) || typeof parsed.answer !== "string" || parsed.answer.trim() === "") {
    return undefined;
  }
  const citations: { tool: string; ref: string }[] = [];
  if (Array.isArray(parsed.citations)) {
    for (const entry of parsed.citations) {
      if (isRecord(entry) && typeof entry.tool === "string" && typeof entry.ref === "string") {
        citations.push({ tool: entry.tool, ref: entry.ref });
      }
    }
  }
  return { answer: parsed.answer, citations, proposals: parseProposals(parsed.proposals) };
}

/** The index's value keeps the tool as its own typed field rather than
 * re-deriving it from the model's string — so a resolved citation's `tool`
 * comes from a ToolRun this code produced, never from a cast of whatever
 * the model wrote. */
interface CitableSource {
  tool: AssistantToolName;
  entity: CitableEntity;
}

function citableIndex(runs: ToolRun[]): Map<string, CitableSource> {
  const index = new Map<string, CitableSource>();
  for (const run of runs) {
    if (run.status !== "ok") {
      // A refused or failed surface contributes nothing citable. This is
      // the line AC3 rests on: no data, therefore no citation, therefore
      // no rendered answer that leans on it.
      continue;
    }
    for (const entity of run.entities) {
      index.set(`${run.tool} ${entity.ref}`, { tool: run.tool, entity });
    }
  }
  return index;
}

/** Splits a reply's citations into the ones this turn's tool results
 * actually support and the ones nothing supports. */
export function resolveCitations(
  citations: { tool: string; ref: string }[],
  runs: ToolRun[],
): { resolved: ResolvedCitation[]; unresolved: UnresolvedCitation[] } {
  const index = citableIndex(runs);
  const resolved: ResolvedCitation[] = [];
  const unresolved: UnresolvedCitation[] = [];
  const seen = new Set<string>();
  for (const citation of citations) {
    const key = `${citation.tool} ${citation.ref}`;
    const source = index.get(key);
    if (source === undefined) {
      unresolved.push({ tool: citation.tool, ref: citation.ref });
      continue;
    }
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    resolved.push({
      tool: source.tool,
      ref: source.entity.ref,
      label: source.entity.label,
      href: source.entity.href,
    });
  }
  return { resolved, unresolved };
}

/**
 * The render decision. Given the raw reply text and the tool runs behind
 * it, returns either an answer WITH its resolving citations, or a withheld
 * result that does not carry the answer text at all.
 */
export function evaluateReply(text: string, runs: ToolRun[]): AssistantResult {
  const reply = parseModelReply(text);
  if (reply === undefined) {
    return { status: "withheld", reason: "unparsable-reply", unresolved: [], runs };
  }
  const { resolved, unresolved } = resolveCitations(reply.citations, runs);
  if (resolved.length === 0) {
    return { status: "withheld", reason: "no-resolving-citation", unresolved, runs };
  }
  return {
    status: "answered",
    answer: reply.answer,
    citations: resolved,
    unresolved,
    proposals: reply.proposals,
    runs,
  };
}
