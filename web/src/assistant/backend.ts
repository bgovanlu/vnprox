// T-2808: the model backend — configurable, and ABSENT BY DEFAULT.
//
// vnprox ships no model backend and no credential for one. Until an
// operator configures one here, the assistant sends nothing anywhere: the
// engine short-circuits before a transport exists (engine.ts), and the
// panel says so in plain words rather than failing at request time.
//
// The configuration lives in the browser, in localStorage, on purpose:
//
//   - The card forbids a new backend capability and a new data path. A
//     server-side setting would be both — a route to write it, a column to
//     hold it, and a daemon that now holds a third-party API credential.
//   - It keeps AC6 structural rather than procedural. Prompts and answers
//     travel browser -> model backend and nowhere else; the daemon never
//     receives them, so it cannot log them and a support bundle cannot
//     carry them (see internal/api's assistant no-daemon-path test and
//     internal/backup's declared-entry assertion).
//
// The cost is honest and belongs in the report: the backend is configured
// per browser, not per cluster, and an operator on a second workstation
// configures it again.
const STORAGE_KEY = "vnprox.assistant.backend";

/** A configured backend. `endpoint` is an OpenAI-style chat-completions URL;
 * `apiKey` is optional because a local runner (llama.cpp, Ollama's OpenAI
 * shim, a reverse proxy that injects the key) usually needs none. */
export interface ModelBackend {
  endpoint: string;
  model: string;
  apiKey?: string;
}

/** Storage seam, so tests drive real behaviour without touching a global.
 * `window.localStorage` satisfies it. */
export interface BackendStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function defaultStore(): BackendStore | undefined {
  try {
    return window.localStorage;
  } catch {
    // A browser with storage disabled: no configured backend, which is the
    // documented default anyway.
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/** Reads the configured backend, or undefined when none is configured —
 * which is the default, and the state a fresh install is in. Malformed or
 * partial stored JSON reads as "absent": a half-configured backend must not
 * become a request to somewhere unintended. */
export function loadModelBackend(store: BackendStore | undefined = defaultStore()): ModelBackend | undefined {
  const raw = store?.getItem(STORAGE_KEY);
  if (raw === null || raw === undefined || raw === "") {
    return undefined;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return undefined;
  }
  if (!isRecord(parsed)) {
    return undefined;
  }
  const { endpoint, model, apiKey } = parsed;
  if (typeof endpoint !== "string" || endpoint === "" || typeof model !== "string" || model === "") {
    return undefined;
  }
  return {
    endpoint,
    model,
    ...(typeof apiKey === "string" && apiKey !== "" ? { apiKey } : {}),
  };
}

/** Persists the endpoint and model — and DELIBERATELY NOT the API key.
 *
 * A key in localStorage is a credential at rest in the browser, readable by
 * anything that can run script in this origin, and it would then also be a
 * credential in any browser-state export. The panel holds the key in memory
 * for the tab instead: an operator re-enters it after a reload, which is
 * the right trade for a surface that is optional in the first place.
 * Asserted by backend.test.ts. */
export function saveModelBackend(backend: ModelBackend, store: BackendStore | undefined = defaultStore()): void {
  store?.setItem(STORAGE_KEY, JSON.stringify({ endpoint: backend.endpoint, model: backend.model }));
}

export function clearModelBackend(store: BackendStore | undefined = defaultStore()): void {
  store?.removeItem(STORAGE_KEY);
}

/** What the engine hands a transport. `context` is the tool evidence; the
 * transport's whole job is to turn this into a reply string. */
export interface ModelRequest {
  question: string;
  /** Compact, already-projected tool results (ToolRun.summary values). */
  context: { tool: string; summary: unknown }[];
  /** Every citable ref this turn produced, so the model is told what it is
   * allowed to cite rather than left to guess (an answer citing anything
   * else is dropped by citations.ts regardless). */
  citableRefs: { tool: string; ref: string; label: string }[];
}

/** The seam AC1 is asserted through: with no backend configured the engine
 * never calls this, and a test can pass a transport that fails if called. */
export type ModelTransport = (req: ModelRequest) => Promise<string>;

/** The reply contract, stated to the model in one place so the parser and
 * the prompt cannot drift apart. */
export const REPLY_CONTRACT = [
  "Answer ONLY from the tool results provided. Reply with a single JSON object:",
  '{"answer": string, "citations": [{"tool": string, "ref": string}], "proposals": [ ... ]}',
  "Every claim must be supported by a citation whose `ref` appears verbatim in citableRefs.",
  "An answer with no such citation will be discarded and never shown, so do not guess.",
  "`proposals` is optional; each entry is either",
  '{"kind":"iface.update","targetRef":string,"mtu"?:number,"addresses"?:string[],"gateway"?:string,"autostart"?:boolean}',
  'or {"kind":"ipam.alloc.create","targetRef":string,"cidr":string,"hostname"?:string}.',
  "A proposal is only ever staged as a DRAFT changeset a human reviews and applies; you cannot apply anything.",
].join("\n");

/** Builds the real HTTP transport for a configured backend: an OpenAI-style
 * chat-completions POST, straight from the browser to the operator's own
 * endpoint. It never touches `/api/v1` (AC6 — vnproxd is not in this path
 * at all) and it is never constructed when no backend is configured. */
export function createHttpModelTransport(backend: ModelBackend, fetchImpl: typeof fetch = fetch): ModelTransport {
  return async (req: ModelRequest): Promise<string> => {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (backend.apiKey !== undefined) {
      headers.Authorization = `Bearer ${backend.apiKey}`;
    }
    const res = await fetchImpl(backend.endpoint, {
      method: "POST",
      headers,
      body: JSON.stringify({
        model: backend.model,
        messages: [
          { role: "system", content: REPLY_CONTRACT },
          {
            role: "user",
            content: JSON.stringify({
              question: req.question,
              toolResults: req.context,
              citableRefs: req.citableRefs,
            }),
          },
        ],
      }),
    });
    if (!res.ok) {
      // The status only. The body of a failed model call can echo the
      // prompt back, and this message is what the panel renders.
      throw new Error(`the model backend answered ${String(res.status)}`);
    }
    const body: unknown = await res.json();
    return extractReplyText(body);
  };
}

/** Pulls the assistant message out of an OpenAI-style response without a
 * cast: anything unexpected becomes an empty string, which the parser then
 * refuses (and an unparsable reply is never rendered). */
export function extractReplyText(body: unknown): string {
  if (!isRecord(body)) {
    return "";
  }
  const choices = body.choices;
  if (!Array.isArray(choices)) {
    return "";
  }
  const first: unknown = choices[0];
  if (!isRecord(first)) {
    return "";
  }
  const message = first.message;
  if (!isRecord(message)) {
    return "";
  }
  return typeof message.content === "string" ? message.content : "";
}
