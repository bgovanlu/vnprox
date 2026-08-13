// T-2808, at the render path — the level the acceptance criteria are
// written about:
//
//   AC1  the panel states plainly that no backend is configured, and with
//        none configured it issues no request at all.
//   AC2  an uncited answer is NOT RENDERED, driven by a fixture reply that
//        produces one (and a control leg where a cited answer renders).
//   AC4  a staged proposal is tagged and hands off to the normal review
//        surface.
//   AC5  the panel offers no apply-shaped control.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AssistantPanel } from "./AssistantPanel";
import { useAssistantStore } from "./store";
import { useChangesetDrawerStore } from "../changesets/store";
import type { Changeset, MeResponse } from "../api/types";

const MODEL_ENDPOINT = "https://model.test.invalid/v1/chat/completions";

const SESSION: MeResponse = {
  user: { username: "alice", realm: "pam" },
  caps: {
    pve1: {
      netRead: true,
      netWrite: true,
      sdnRead: true,
      sdnWrite: true,
      fwRead: true,
      fwWrite: true,
      guestNet: true,
      audit: true,
      capture: true,
    },
  },
};

vi.mock("../api/useSession", () => ({
  SESSION_QUERY_KEY: ["auth", "me"],
  useSession: () => ({ data: SESSION }),
}));

vi.mock("../api/changesets", () => ({
  createChangeset: vi.fn(),
  listChangesets: vi.fn(() => Promise.resolve([])),
  getChangeset: vi.fn(),
  updateChangeset: vi.fn(),
  discardChangeset: vi.fn(),
  validateChangeset: vi.fn(),
  diffChangeset: vi.fn(),
  changesetImpact: vi.fn(),
  applyChangeset: vi.fn(),
  confirmChangeset: vi.fn(),
  rollbackChangeset: vi.fn(),
  addChangesetComment: vi.fn(),
  deleteChangesetComment: vi.fn(),
  reviewApproveChangeset: vi.fn(),
  reviewRejectChangeset: vi.fn(),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

import * as changesetsApi from "../api/changesets";

/** Answers the read tools with one entity each, and the model endpoint with
 * whatever `reply` the test wants. Records every URL so AC1's "no request"
 * is an observation. */
function stubFetch(reply: string): { urls: string[] } {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string | URL | Request): Promise<Response> => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      urls.push(url);
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
      return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }));
    }),
  );
  return { urls };
}

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

function Harness({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/topology"]}>
        {children}
        <Routes>
          <Route path="*" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

function configureBackend(): void {
  window.localStorage.setItem(
    "vnprox.assistant.backend",
    JSON.stringify({ endpoint: MODEL_ENDPOINT, model: "test-model" }),
  );
}

function citedReply(answer: string, extra: Record<string, unknown> = {}): string {
  return JSON.stringify({
    answer,
    citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr0" }],
    ...extra,
  });
}

/** Fills the question box and submits. `fireEvent.change` rather than
 * `user.type` deliberately: typing a sentence key-by-key costs seconds per
 * test in jsdom, and nothing here is about keystroke handling. */
async function ask(user: ReturnType<typeof userEvent.setup>, question: string): Promise<void> {
  fireEvent.change(screen.getByRole("textbox", { name: /question/i }), { target: { value: question } });
  await user.click(screen.getByRole("button", { name: "Ask" }));
}

beforeEach(() => {
  window.localStorage.clear();
  useAssistantStore.setState({ open: true });
  useChangesetDrawerStore.setState({
    activeId: undefined,
    drawerOpen: false,
    reviewRequested: false,
    warningsAcknowledged: false,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("AC1 — absent backend by default", () => {
  it("says so plainly and sends nothing", async () => {
    const { urls } = stubFetch(citedReply("never reached"));
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    expect(screen.getByText("No model backend is configured.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Ask" })).toBeDisabled();
    expect(screen.getByRole("textbox", { name: /question/i })).toBeDisabled();
    await waitFor(() => {
      expect(urls).toHaveLength(0);
    });
  });

  it("CONTROL: with a backend configured the panel drops the notice and can ask", async () => {
    configureBackend();
    const { urls } = stubFetch(citedReply("vmbr0 is up"));
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    expect(screen.queryByText("No model backend is configured.")).not.toBeInTheDocument();
    await ask(user, "what does the network look like?");
    await waitFor(() => {
      expect(urls).toContain(MODEL_ENDPOINT);
    });
  });
});

describe("AC2 — an uncited answer is not rendered", () => {
  it("refuses a fixture reply whose citation resolves to nothing", async () => {
    configureBackend();
    stubFetch(
      JSON.stringify({
        answer: "vmbr7 is misconfigured and you should delete it",
        citations: [{ tool: "topology.get", ref: "bridge:pve1:vmbr7-not-real" }],
      }),
    );
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");

    await waitFor(() => {
      expect(screen.getByText("Answer withheld")).toBeInTheDocument();
    });
    expect(screen.queryByText(/vmbr7 is misconfigured/)).not.toBeInTheDocument();
    // The fabricated citation is named, so the operator sees WHY.
    expect(screen.getByText(/bridge:pve1:vmbr7-not-real/)).toBeInTheDocument();
  });

  it("refuses a reply with no citations at all", async () => {
    configureBackend();
    stubFetch(JSON.stringify({ answer: "Everything looks fine to me!", citations: [] }));
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");

    await waitFor(() => {
      expect(screen.getByText("Answer withheld")).toBeInTheDocument();
    });
    expect(screen.queryByText(/Everything looks fine/)).not.toBeInTheDocument();
  });

  it("refuses prose that ignores the reply contract", async () => {
    configureBackend();
    stubFetch("Sure — vmbr0 is fine, nothing to worry about.");
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");

    await waitFor(() => {
      expect(screen.getByText("Answer withheld")).toBeInTheDocument();
    });
    expect(screen.queryByText(/nothing to worry about/)).not.toBeInTheDocument();
  });

  it("CONTROL: an answer citing a real entity renders, with a link into the product", async () => {
    configureBackend();
    stubFetch(citedReply("vmbr0 is up and carries no VLAN tag"));
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");

    await waitFor(() => {
      expect(screen.getByText("vmbr0 is up and carries no VLAN tag")).toBeInTheDocument();
    });
    expect(screen.getByRole("link", { name: /vmbr0 \(bridge\)/ })).toHaveAttribute("href", "/topology");
    expect(screen.queryByText("Answer withheld")).not.toBeInTheDocument();
  });
});

describe("AC4 — a staged proposal is tagged and lands in normal review", () => {
  it("stages through POST /changesets with the assistant tag and hands off to review", async () => {
    configureBackend();
    stubFetch(
      citedReply("vmbr0's MTU is too low for this storage network", {
        proposals: [{ kind: "iface.update", targetRef: "bridge:pve1:vmbr0", mtu: 9000 }],
      }),
    );
    const staged: Changeset = {
      id: "cs-assistant-1",
      title: "[assistant] update bridge:pve1:vmbr0: MTU 9000",
      author: "alice",
      status: "draft",
      ops: [{ op: "iface.update", target: "bridge:pve1:vmbr0", params: { mtu: 9000 } }],
      findings: [],
      createdAt: 0,
      updatedAt: 0,
    };
    vi.mocked(changesetsApi.createChangeset).mockResolvedValue(staged);

    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");
    await waitFor(() => {
      expect(screen.getByText("Proposed change")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Stage for review" }));

    await waitFor(() => {
      expect(changesetsApi.createChangeset).toHaveBeenCalledTimes(1);
    });
    const call = vi.mocked(changesetsApi.createChangeset).mock.calls[0]?.[0];
    expect(call?.title.startsWith("[assistant]")).toBe(true);
    expect(call?.ops).toEqual([{ op: "iface.update", target: "bridge:pve1:vmbr0", params: { mtu: 9000 } }]);

    // Lands in normal review: the draft becomes the changeset drawer's
    // active changeset and the app navigates to the review screen.
    await waitFor(() => {
      expect(useChangesetDrawerStore.getState().activeId).toBe("cs-assistant-1");
    });
    expect(screen.getByTestId("location")).toHaveTextContent("/changesets/cs-assistant-1/review");
  });
});

describe("AC5 — no apply path in the panel", () => {
  it("offers staging and nothing that applies", async () => {
    configureBackend();
    stubFetch(
      citedReply("vmbr0's MTU is too low", {
        proposals: [{ kind: "iface.update", targetRef: "bridge:pve1:vmbr0", mtu: 9000 }],
      }),
    );
    const user = userEvent.setup();
    render(
      <Harness>
        <AssistantPanel />
      </Harness>,
    );

    await ask(user, "what does the network look like?");
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Stage for review" })).toBeInTheDocument();
    });

    for (const button of screen.getAllByRole("button")) {
      expect(button.textContent).not.toMatch(/apply|confirm|roll ?back/i);
    }
  });
});
