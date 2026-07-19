// Shared test-only harness for ConnectClustersWizard.test.tsx — mirrors
// sdn/wizards/wizardTestUtils.tsx's exact shape (a `vi.stubGlobal("fetch",
// ...)` mock covering every route the wizard's hooks touch, capturing
// POST /changesets bodies) but scoped to this wizard's own routes: GET
// /auth/me, GET /topology (one node, enough for the source-node picker),
// GET /firewall/rulesets?scope=node&node=... (the append-position lookup),
// and POST /changesets.
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset, MeResponse, Op, RulesetView, TopologyResponse } from "../api/types";

export const sessionWithNetWrite: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
    "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
  },
};

export function oneNodeTopology(): TopologyResponse {
  return {
    nodes: [{ id: "node:pve1:pve1", kind: "node", label: "pve1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] }],
    edges: [],
    layers: ["phys", "l2", "sdn", "guest"],
    generatedAt: 1_752_000_000,
  };
}

function emptyNodeRuleset(node: string): RulesetView {
  return { ref: `fw-ruleset:${node}:node`, scope: "node", node, enabled: true, rules: [] };
}

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export interface WgWizardFetchStub {
  /** Every POST /changesets request body captured, in order. */
  postedChangesets: { title: string; ops: Op[] }[];
  /** Every route this stub was asked for but doesn't recognize — a test
   * asserting AC4's "no other mutating route" regression checks this stays
   * free of anything but GETs. */
  requestedUrls: { url: string; method: string }[];
}

export function stubWgWizardFetch(): WgWizardFetchStub {
  const stub: WgWizardFetchStub = { postedChangesets: [], requestedUrls: [] };
  const topology = oneNodeTopology();

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlOf(input);
      const method = init?.method ?? "GET";
      stub.requestedUrls.push({ url, method });

      if (url.includes("/auth/me")) {
        return Promise.resolve(jsonResponse(sessionWithNetWrite));
      }
      if (url.includes("/topology")) {
        return Promise.resolve(jsonResponse(topology));
      }
      if (url.includes("/firewall/rulesets") && url.includes("scope=node")) {
        const node = new URL(url, "https://vnprox.test").searchParams.get("node") ?? "pve1";
        return Promise.resolve(jsonResponse(emptyNodeRuleset(node)));
      }
      if (url.includes("/changesets") && method === "POST") {
        const rawBody = typeof init?.body === "string" ? init.body : "";
        const body = rawBody ? (JSON.parse(rawBody) as { title: string; ops: Op[] }) : { title: "", ops: [] };
        stub.postedChangesets.push(body);
        const created: Changeset = {
          id: `cs-${String(stub.postedChangesets.length)}`,
          title: body.title,
          author: "root@pam",
          status: "draft",
          ops: body.ops,
          findings: [],
          createdAt: 1_752_000_000,
          updatedAt: 1_752_000_000,
        };
        return Promise.resolve(jsonResponse(created));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  return stub;
}

export function renderWithProviders(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>{ui}</ToastProvider>
    </QueryClientProvider>,
  );
}
