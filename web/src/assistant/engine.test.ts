// T-2808 acceptance criteria 1, 3 and 6, asserted against the real engine
// driving the real api layer (the tool calls below go through
// api/topology.ts etc. and out through a stubbed global fetch, exactly as
// they do in the browser).
//
// Every negative assertion here has a control leg, because a negative
// assertion without one is a test that passes when the mechanism it is
// watching has stopped running:
//
//   AC1 — "no outbound request with no backend configured" is paired with a
//         leg where a backend IS configured and the same transport records
//         a call. Without it, a transport that could never be called would
//         pass.
//   AC3 — every "a caller lacking the capability does not reach this
//         surface" row is paired with a capable caller reaching it.
//   AC6 — every "the prompt is not in X" is paired with the prompt being
//         found where it IS supposed to be (the model request body).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ask } from "./engine";
import { createHttpModelTransport, type ModelBackend, type ModelTransport } from "./backend";
import type { Capabilities } from "../api/types";

const MODEL_ENDPOINT = "https://model.test.invalid/v1/chat/completions";
const BACKEND: ModelBackend = { endpoint: MODEL_ENDPOINT, model: "test-model" };

const ALL_CAPS: Capabilities = {
  netRead: true,
  netWrite: true,
  sdnRead: true,
  sdnWrite: true,
  fwRead: true,
  fwWrite: true,
  guestNet: true,
  audit: true,
  capture: true,
};

function capsWithout(...missing: (keyof Capabilities)[]): Record<string, Capabilities> {
  const node: Capabilities = { ...ALL_CAPS };
  for (const cap of missing) {
    node[cap] = false;
  }
  return { pve1: node };
}

const FULL_CAPS = capsWithout();

interface FetchCall {
  url: string;
  body: string;
}

/** A fetch stub that answers each mirrored read route with a fixture, and
 * the model endpoint with an OpenAI-shaped reply. Records every call so a
 * test can assert on what was and was not sent. */
function stubFetch(options: {
  reply?: string;
  forbid?: string[];
} = {}): { calls: FetchCall[] } {
  const calls: FetchCall[] = [];
  const reply = options.reply ?? JSON.stringify({ answer: "ok", citations: [] });
  const forbid = options.forbid ?? [];

  const impl = vi.fn((input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const body = typeof init?.body === "string" ? init.body : "";
    calls.push({ url, body });

    if (forbid.some((f) => url.startsWith(f))) {
      return Promise.resolve(
        new Response(JSON.stringify({ error: { code: "forbidden", message: "no" } }), { status: 403 }),
      );
    }
    if (url === MODEL_ENDPOINT) {
      return Promise.resolve(
        new Response(JSON.stringify({ choices: [{ message: { content: reply } }] }), { status: 200 }),
      );
    }
    if (url.startsWith("/api/v1/topology")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            nodes: [
              {
                id: "bridge:pve1:vmbr0",
                kind: "bridge",
                label: "vmbr0",
                layer: "l2",
                nodeGroup: "pve1",
                status: "ok",
                badges: [],
              },
            ],
            edges: [],
            layers: ["l2"],
            generatedAt: 1,
          }),
          { status: 200 },
        ),
      );
    }
    if (url.startsWith("/api/v1/findings")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            items: [
              {
                id: "finding-1",
                source: "drift",
                check: "iface_drift",
                severity: "warning",
                detail: "vmbr0 differs",
                nodes: ["pve1"],
                fixable: false,
              },
            ],
          }),
          { status: 200 },
        ),
      );
    }
    if (url.startsWith("/api/v1/flows")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            items: [
              {
                at: 1,
                node: "pve1",
                srcIp: "10.0.0.1",
                dstIp: "10.0.0.2",
                proto: 6,
                bytes: 1,
                packets: 1,
                srcRef: "bridge:pve1:vmbr0",
                source: "conntrack",
              },
            ],
          }),
          { status: 200 },
        ),
      );
    }
    if (url.startsWith("/api/v1/ipam/subnets")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            items: [
              {
                cidr: "10.0.0.0/24",
                source: "sdn",
                total: 254,
                allocated: 200,
                observed: 0,
                conflicts: 0,
                utilization: 0.79,
              },
            ],
            generatedAt: 1,
          }),
          { status: 200 },
        ),
      );
    }
    if (url.startsWith("/api/v1/simulate/path")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            verdict: "allow",
            src: { kind: "ip" },
            dst: { kind: "ip" },
            hops: [],
            caveats: [],
          }),
          { status: 200 },
        ),
      );
    }
    if (url.startsWith("/api/v1/diagnose")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            target: "bridge:pve1:vmbr0",
            steps: [],
            verdict: { summary: "looks fine", confidence: "medium", linkedFindingIds: [] },
          }),
          { status: 200 },
        ),
      );
    }
    return Promise.resolve(new Response("{}", { status: 200 }));
  });

  vi.stubGlobal("fetch", impl);
  return { calls };
}

/** The AC1 transport: it fails the test if it is ever called, and records
 * that it was, so the control leg can assert the opposite. */
function transportThatMustNotBeCalled(): { transport: ModelTransport; called: () => boolean } {
  let called = false;
  const transport: ModelTransport = () => {
    called = true;
    throw new Error("the model transport was called — nothing may be sent with no backend configured");
  };
  return { transport, called: () => called };
}

const FULL_QUESTION =
  "why is bridge:pve1:vmbr0 down, what traffic and subnets are involved, and can 10.0.0.1 reach 10.0.0.2?";

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("AC1 — nothing is sent when no model backend is configured", () => {
  it("never calls the transport, and issues no request at all", async () => {
    const { calls } = stubFetch();
    const { transport, called } = transportThatMustNotBeCalled();

    const result = await ask({ question: FULL_QUESTION, caps: FULL_CAPS, backend: undefined, transport });

    expect(result).toEqual({ status: "no-backend" });
    expect(called()).toBe(false);
    // Not even the read tools ran: with nothing to answer with, reading the
    // cluster would be work for no one.
    expect(calls).toHaveLength(0);
  });

  it("CONTROL: the same transport IS called once a backend is configured", async () => {
    const { calls } = stubFetch({
      reply: JSON.stringify({
        answer: "vmbr0 is up",
        citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
      }),
    });
    let called = false;
    const transport: ModelTransport = () => {
      called = true;
      return Promise.resolve(
        JSON.stringify({
          answer: "vmbr0 is up",
          citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
        }),
      );
    };

    const result = await ask({ question: FULL_QUESTION, caps: FULL_CAPS, backend: BACKEND, transport });

    expect(called).toBe(true);
    expect(result.status).toBe("answered");
    // And the read tools did run — so the "no request at all" above was a
    // real observation about the no-backend path, not an inert stub.
    expect(calls.length).toBeGreaterThan(0);
  });
});

describe("AC3 — the panel's authorisation is the caller's own", () => {
  const rows: { tool: string; cap: keyof Capabilities; route: string }[] = [
    { tool: "topology.get", cap: "netRead", route: "/api/v1/topology" },
    { tool: "findings.list", cap: "netRead", route: "/api/v1/findings" },
    { tool: "flows.query", cap: "netRead", route: "/api/v1/flows" },
    { tool: "ipam.subnets.list", cap: "sdnRead", route: "/api/v1/ipam/subnets" },
    { tool: "simulate.path", cap: "netRead", route: "/api/v1/simulate/path" },
    { tool: "diagnose.run", cap: "netRead", route: "/api/v1/diagnose" },
  ];

  for (const row of rows) {
    it(`${row.tool}: a caller without ${row.cap} never reaches ${row.route}`, async () => {
      const { calls } = stubFetch();
      const transport: ModelTransport = () => Promise.resolve(JSON.stringify({ answer: "x", citations: [] }));

      await ask({
        question: FULL_QUESTION,
        caps: capsWithout(row.cap),
        backend: BACKEND,
        transport,
      });

      expect(calls.filter((c) => c.url.startsWith(row.route))).toHaveLength(0);
    });

    it(`${row.tool}: CONTROL — a caller with ${row.cap} does reach ${row.route}`, async () => {
      const { calls } = stubFetch();
      const transport: ModelTransport = () => Promise.resolve(JSON.stringify({ answer: "x", citations: [] }));

      await ask({ question: FULL_QUESTION, caps: FULL_CAPS, backend: BACKEND, transport });

      expect(calls.filter((c) => c.url.startsWith(row.route)).length).toBeGreaterThan(0);
    });
  }

  it("the SERVER is the authority: a 403 surface contributes nothing citable, so the answer is withheld", async () => {
    // The client-side capability filter is bypassed here on purpose (full
    // caps), leaving the server's own refusal as the only gate — which is
    // the arrangement in production if the two ever disagree.
    stubFetch({ forbid: ["/api/v1/ipam/subnets"] });
    const transport: ModelTransport = () =>
      Promise.resolve(
        JSON.stringify({
          answer: "10.0.0.0/24 is 79% full",
          citations: [{ tool: "ipam.subnets.list", ref: "10.0.0.0/24" }],
        }),
      );

    const result = await ask({
      question: "which subnets are nearly full?",
      caps: FULL_CAPS,
      backend: BACKEND,
      transport,
    });

    expect(result.status).toBe("withheld");
    if (result.status === "withheld") {
      expect(result.reason).toBe("no-resolving-citation");
      const ipam = result.runs.find((r) => r.tool === "ipam.subnets.list");
      expect(ipam?.status).toBe("refused");
      expect(ipam?.entities).toHaveLength(0);
    }
  });

  it("CONTROL: the same question answers when the server allows the surface", async () => {
    stubFetch();
    const transport: ModelTransport = () =>
      Promise.resolve(
        JSON.stringify({
          answer: "10.0.0.0/24 is 79% full",
          citations: [{ tool: "ipam.subnets.list", ref: "10.0.0.0/24" }],
        }),
      );

    const result = await ask({
      question: "which subnets are nearly full?",
      caps: FULL_CAPS,
      backend: BACKEND,
      transport,
    });

    expect(result.status).toBe("answered");
  });
});

describe("AC6 — prompt content and answers stay out of vnprox", () => {
  const PROMPT_CANARY = "CANARY-T2808-PROMPT-DO-NOT-LEAK";
  const ANSWER_CANARY = "CANARY-T2808-ANSWER-DO-NOT-LEAK";

  it("sends the question only to the model backend, never to /api/v1", async () => {
    const { calls } = stubFetch({
      reply: JSON.stringify({
        answer: ANSWER_CANARY,
        citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
      }),
    });

    const result = await ask({
      question: `${PROMPT_CANARY} which subnets are nearly full?`,
      caps: FULL_CAPS,
      backend: BACKEND,
      // The REAL transport, so the model request goes through the same
      // fetch this test is watching.
      transport: createHttpModelTransport(BACKEND),
    });

    expect(result.status).toBe("answered");

    const daemonCalls = calls.filter((c) => c.url.startsWith("/api/v1"));
    // Non-vacuity: the daemon really was called, so "not in any daemon
    // request" is an observation and not an empty set.
    expect(daemonCalls.length).toBeGreaterThan(0);
    for (const call of daemonCalls) {
      expect(call.body).not.toContain(PROMPT_CANARY);
      expect(call.url).not.toContain(PROMPT_CANARY);
    }

    // CONTROL: the scan does find the prompt where it is supposed to be.
    const modelCalls = calls.filter((c) => c.url === MODEL_ENDPOINT);
    expect(modelCalls).toHaveLength(1);
    expect(modelCalls[0]?.body).toContain(PROMPT_CANARY);
  });

  it("never writes the question or the answer to browser storage", async () => {
    stubFetch({
      reply: JSON.stringify({
        answer: ANSWER_CANARY,
        citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
      }),
    });
    // A stored backend config, so the storage scan below has something to
    // read — and so its "found nothing" result is not vacuous.
    window.localStorage.setItem(
      "vnprox.assistant.backend",
      JSON.stringify({ endpoint: MODEL_ENDPOINT, model: "test-model" }),
    );

    await ask({
      question: `${PROMPT_CANARY} which subnets are nearly full?`,
      caps: FULL_CAPS,
      backend: BACKEND,
      transport: createHttpModelTransport(BACKEND),
    });

    const stored: string[] = [];
    for (let i = 0; i < window.localStorage.length; i++) {
      const key = window.localStorage.key(i);
      if (key !== null) {
        stored.push(`${key}=${window.localStorage.getItem(key) ?? ""}`);
      }
    }
    expect(stored.length).toBeGreaterThan(0);
    expect(stored.join("\n")).toContain(MODEL_ENDPOINT); // control
    expect(stored.join("\n")).not.toContain(PROMPT_CANARY);
    expect(stored.join("\n")).not.toContain(ANSWER_CANARY);
  });

  it("never logs the question or the answer to the console, including on a backend failure", async () => {
    const spies = {
      log: vi.spyOn(console, "log").mockImplementation(() => undefined),
      info: vi.spyOn(console, "info").mockImplementation(() => undefined),
      warn: vi.spyOn(console, "warn").mockImplementation(() => undefined),
      error: vi.spyOn(console, "error").mockImplementation(() => undefined),
      debug: vi.spyOn(console, "debug").mockImplementation(() => undefined),
    };
    // A backend that fails, echoing the prompt in its error body — the case
    // where a careless implementation surfaces the prompt in a log line.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL | Request): Promise<Response> => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
        if (url === MODEL_ENDPOINT) {
          return Promise.resolve(new Response(`upstream rejected: ${PROMPT_CANARY}`, { status: 500 }));
        }
        return Promise.resolve(
          new Response(
            JSON.stringify({
              nodes: [
                {
                  id: "bridge:pve1:vmbr0",
                  kind: "bridge",
                  label: "vmbr0",
                  layer: "l2",
                  nodeGroup: "pve1",
                  status: "ok",
                  badges: [],
                },
              ],
              edges: [],
              layers: ["l2"],
              generatedAt: 1,
              items: [],
            }),
            { status: 200 },
          ),
        );
      }),
    );

    const result = await ask({
      question: `${PROMPT_CANARY} what is wrong?`,
      caps: FULL_CAPS,
      backend: BACKEND,
      transport: createHttpModelTransport(BACKEND),
    });

    expect(result.status).toBe("error");
    if (result.status === "error") {
      // The surfaced message names the status, not the prompt the upstream
      // echoed back.
      expect(result.message).not.toContain(PROMPT_CANARY);
      expect(result.message).toContain("500");
    }
    for (const spy of Object.values(spies)) {
      for (const call of spy.mock.calls) {
        expect(JSON.stringify(call)).not.toContain(PROMPT_CANARY);
      }
    }
    // CONTROL: the spies are live — a console.warn here is seen.
    console.warn("assistant-test-control");
    expect(spies.warn).toHaveBeenCalledWith("assistant-test-control");
  });
});
