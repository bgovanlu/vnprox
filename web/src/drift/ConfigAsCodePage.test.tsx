// T-3001's screen tests. The claims they pin are the ones the card is about,
// not that the markup rendered:
//
//   * an unconfigured git sync says so, and offers nothing that would fail;
//   * "we could not ask" never renders as "it is off";
//   * a spec/config/live disagreement shows all three positions AND both
//     reconciliation actions;
//   * one click never produces both actions — each needs its own confirm;
//   * a failing sync shows the daemon's own message, not a generic one;
//   * a clean plan states its two facts separately.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { GitSyncStatus, SpecProposal } from "../api/gitsync";
import type { SpecExport, SpecImportResult, SpecPin } from "../api/spec";
import type { Changeset, DriftFinding, MeResponse } from "../api/types";
import { ApiError } from "../api/client";
import { ToastProvider } from "../components/Toast";
import { ConfigAsCodePage } from "./ConfigAsCodePage";

const fetchGitSyncStatus = vi.fn<() => Promise<GitSyncStatus>>();
const fetchDrift = vi.fn<() => Promise<DriftFinding[]>>();
const fetchAdoption = vi.fn<(id: string) => Promise<SpecProposal | null>>();
const restoreIntent = vi.fn<(id: string) => Promise<Changeset>>();
const adoptReality = vi.fn<(id: string) => Promise<SpecProposal>>();
const fetchSpec = vi.fn<() => Promise<SpecExport>>();
const fetchSpecPin = vi.fn<() => Promise<SpecPin>>();
const importSpec = vi.fn<(content: string) => Promise<SpecImportResult>>();

vi.mock("../api/gitsync", async () => {
  const actual = await vi.importActual<typeof import("../api/gitsync")>("../api/gitsync");
  return { ...actual, fetchGitSyncStatus: () => fetchGitSyncStatus() };
});

vi.mock("../api/drift", async () => {
  const actual = await vi.importActual<typeof import("../api/drift")>("../api/drift");
  return {
    ...actual,
    fetchDrift: () => fetchDrift(),
    fetchAdoption: (id: string) => fetchAdoption(id),
    restoreIntent: (id: string) => restoreIntent(id),
    adoptReality: (id: string) => adoptReality(id),
  };
});

vi.mock("../api/spec", async () => {
  const actual = await vi.importActual<typeof import("../api/spec")>("../api/spec");
  return {
    ...actual,
    fetchSpec: () => fetchSpec(),
    fetchSpecPin: () => fetchSpecPin(),
    importSpec: (content: string) => importSpec(content),
  };
});

// The drift list stays live over a WS bridge; the socket itself is not what
// these tests are about (api/ws.test.ts owns that), and jsdom has no server.
vi.mock("../api/ws", async () => {
  const actual = await vi.importActual<typeof import("../api/ws")>("../api/ws");
  return {
    ...actual,
    createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  };
});

const writer: MeResponse = {
  user: { username: "brian@pam", realm: "pam" },
  caps: {
    "": {
      netRead: true,
      netWrite: true,
      sdnRead: true,
      sdnWrite: true,
      fwRead: true,
      fwWrite: true,
      guestNet: true,
      audit: true,
      capture: false,
    },
  },
};

vi.mock("../api/useSession", () => ({ useSession: () => ({ data: writer }) }));

/** A finding where spec, config and live all have something different to say
 * about one bridge, and both actions are on offer. */
const disagreement: DriftFinding = {
  id: "spec_reconciliation|bridge:pve1:vmbr0",
  check: "spec_reconciliation",
  severity: "error",
  detail: "spec, config and live disagree about vmbr0 on pve1",
  nodes: ["pve1"],
  refs: ["bridge:pve1:vmbr0"],
  fixable: false,
  reconciliation: {
    ref: "bridge:pve1:vmbr0",
    inSpec: true,
    inConfig: true,
    inLive: false,
    fields: [
      {
        field: "mtu",
        values: [
          { position: "spec", value: "9000", known: true },
          { position: "config", value: "1500", known: true },
          // The kernel never reported it: not a value, and not agreement.
          { position: "live", value: "", known: false },
        ],
        differs: ["spec/config"],
      },
    ],
    pairs: [
      { a: "spec", b: "config", fields: ["mtu"], comparable: true },
      { a: "config", b: "live", fields: [], comparable: false },
      { a: "spec", b: "live", fields: [], comparable: false },
    ],
    actions: { adoptReality: true, restoreIntent: true },
  },
};

const enabledStatus: GitSyncStatus = {
  enabled: true,
  remote: "https://github.com/org/infra (github)",
  ref: "main",
  path: "network/cluster.yaml",
  pollIntervalSeconds: 300,
  requireSignedCommits: true,
  lastFetchedSha: "abcdef0123456789",
  lastFetchAt: 1_754_000_000,
  lastSuccessAt: 1_754_000_000,
  lastSigner: "ops@example.com",
  planOpCount: 0,
};

const draft: Changeset = {
  id: "01JDRAFT",
  title: "Restore intent for bridge:pve1:vmbr0",
  author: "brian@pam",
  status: "draft",
  ops: [],
  findings: [],
  createdAt: 1_754_000_100,
  updatedAt: 1_754_000_100,
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter>
          <ConfigAsCodePage />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  // Call history, explicitly: several assertions below are "this mutation was
  // NOT called", which a leftover call from a previous test would falsify.
  vi.clearAllMocks();
  fetchGitSyncStatus.mockResolvedValue({ enabled: false, requireSignedCommits: false, planOpCount: 0 });
  fetchDrift.mockResolvedValue([]);
  fetchAdoption.mockResolvedValue(null);
  fetchSpecPin.mockResolvedValue({ pinned: false });
  fetchSpec.mockResolvedValue({ specVersion: 1, content: "specVersion: 1\nbridges: []\n" });
});

describe("config-as-code cockpit — an unconfigured git sync (AC1)", () => {
  it("says the sync is not configured and offers no control at all", async () => {
    renderPage();

    expect(await screen.findByText("Git sync is not configured")).toBeInTheDocument();
    // Nothing in this panel can be clicked, so nothing in it can 5xx.
    const panel = screen.getByRole("region", { name: /git sync/i });
    expect(panel.querySelectorAll("button")).toHaveLength(1); // the help "?" anchor only
    expect(within(panel).getByRole("button", { name: /^Help:/ })).toBeInTheDocument();
  });

  it("offers no adopt-reality control, because there is no repository to commit to", async () => {
    fetchDrift.mockResolvedValue([disagreement]);
    renderPage();

    // The finding itself still offers the action; the deployment cannot.
    expect(await screen.findByRole("button", { name: "Restore intent…" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Adopt reality…" })).not.toBeInTheDocument();
    expect(screen.getByText(/Adopting reality is unavailable/)).toBeInTheDocument();
  });

  it("reports no spec position rather than agreement when nothing is pinned either", async () => {
    renderPage();

    expect(await screen.findByText("There is no spec position yet")).toBeInTheDocument();
    expect(screen.queryByText("Spec, config and live agree")).not.toBeInTheDocument();
  });
});

describe("config-as-code cockpit — an unreadable status is not a disabled one", () => {
  it("says the status could not be read, and does not claim the sync is off", async () => {
    fetchGitSyncStatus.mockRejectedValue(new ApiError(403, "forbidden", "netRead capability required"));
    renderPage();

    expect(await screen.findByText("Could not read the git sync status")).toBeInTheDocument();
    expect(screen.getByText("netRead capability required")).toBeInTheDocument();
    expect(screen.queryByText("Git sync is not configured")).not.toBeInTheDocument();
  });
});

describe("config-as-code cockpit — a failing sync (AC4)", () => {
  it("renders the daemon's own error message", async () => {
    const message = 'fetching refs/heads/nope: remote: Repository not found (exit status 128)';
    fetchGitSyncStatus.mockResolvedValue({ ...enabledStatus, lastError: message });
    renderPage();

    expect(await screen.findByText("The last sync cycle failed")).toBeInTheDocument();
    expect(screen.getByText(message)).toBeInTheDocument();
    // Not the generic phrasing, and not "not configured" either.
    expect(screen.queryByText("Git sync is not configured")).not.toBeInTheDocument();
  });
});

describe("config-as-code cockpit — a three-way disagreement (AC2)", () => {
  beforeEach(() => {
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    fetchDrift.mockResolvedValue([disagreement]);
  });

  it("renders all three positions, all three pairs, and both actions", async () => {
    renderPage();

    expect(await screen.findByText("Spec: declared")).toBeInTheDocument();
    expect(screen.getByText("Config: present")).toBeInTheDocument();
    expect(screen.getByText("Live: absent")).toBeInTheDocument();

    // The field table keeps "never reported" distinct from a value.
    expect(screen.getByText("9000")).toBeInTheDocument();
    expect(screen.getByText("1500")).toBeInTheDocument();
    expect(screen.getByText("not reported")).toBeInTheDocument();

    // All three pairs, including the ones that are not a two-way diff.
    expect(screen.getByText("differ on mtu")).toBeInTheDocument();
    expect(
      screen.getAllByText("nothing to compare — neither position reported a field the other did"),
    ).toHaveLength(2);

    // Two separate actions. Never one "reconcile".
    expect(screen.getByRole("button", { name: "Restore intent…" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Adopt reality…" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /reconcile/i })).not.toBeInTheDocument();
  });
});

describe("config-as-code cockpit — the two actions are separately confirmed (AC3)", () => {
  beforeEach(() => {
    fetchGitSyncStatus.mockResolvedValue(enabledStatus);
    fetchDrift.mockResolvedValue([disagreement]);
    restoreIntent.mockResolvedValue(draft);
    adoptReality.mockResolvedValue({
      changesetId: "",
      findingId: disagreement.id,
      remote: "https://github.com/org/infra (github)",
      branch: "vnprox/adopt-bridge-pve1-vmbr0",
      path: "network/cluster.yaml",
      pullRequestId: "42",
      pullRequestUrl: "https://github.com/org/infra/pull/42",
      created: true,
    });
  });

  it("does nothing on the first click, and then performs only the action confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Restore intent…" }));

    // One click has staged nothing and proposed nothing.
    expect(restoreIntent).not.toHaveBeenCalled();
    expect(adoptReality).not.toHaveBeenCalled();

    // And the confirmation that opened is unambiguously the cluster-moving
    // one: the other action is not reachable from inside it.
    expect(screen.getByText("Restore intent on the cluster?")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Adopt reality" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Stage the draft" }));

    await waitFor(() => {
      expect(restoreIntent).toHaveBeenCalledWith(disagreement.id);
    });
    expect(restoreIntent).toHaveBeenCalledTimes(1);
    expect(adoptReality).not.toHaveBeenCalled();
  });

  it("proposes only the document change when adopt reality is the one confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Adopt reality…" }));
    expect(screen.getByText("Adopt reality into the document?")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Stage the draft" })).not.toBeInTheDocument();
    expect(adoptReality).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Adopt reality" }));

    await waitFor(() => {
      expect(adoptReality).toHaveBeenCalledWith(disagreement.id);
    });
    expect(adoptReality).toHaveBeenCalledTimes(1);
    expect(restoreIntent).not.toHaveBeenCalled();
  });

  it("shows the daemon's refusal when adopting is not configured on this deployment", async () => {
    const user = userEvent.setup();
    adoptReality.mockRejectedValue(
      new ApiError(
        501,
        "not_implemented",
        "adopting live state into the spec repository is not configured on this deployment ([gitsync] push_token_file)",
      ),
    );
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Adopt reality…" }));
    await user.click(screen.getByRole("button", { name: "Adopt reality" }));

    expect(
      await screen.findByText(
        "adopting live state into the spec repository is not configured on this deployment ([gitsync] push_token_file)",
      ),
    ).toBeInTheDocument();
  });
});

describe("config-as-code cockpit — the plan states two facts", () => {
  it("renders ops == 0 and notInSpec == 0 as the two distinct facts they are", async () => {
    const user = userEvent.setup();
    importSpec.mockResolvedValue({ ...draft, id: "01JPLAN", title: "Spec import", notInSpec: [] });
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Render the live cluster" }));
    await waitFor(() => {
      expect(screen.getByLabelText("Spec document")).toHaveValue("specVersion: 1\nbridges: []\n");
    });

    // The label says what the button does — planning stages a draft.
    await user.click(screen.getByRole("button", { name: "Plan against live state (stages a draft)" }));

    expect(
      await screen.findByText("No operations: live state already matches everything this document declares."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Nothing undeclared: every managed entity this cluster has appears in the document."),
    ).toBeInTheDocument();
    expect(importSpec).toHaveBeenCalledWith("specVersion: 1\nbridges: []\n");
  });

  it("shows the daemon's own rejection of a document it cannot parse", async () => {
    const user = userEvent.setup();
    importSpec.mockRejectedValue(new ApiError(400, "validation_failed", "unsupported specVersion 2"));
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Render the live cluster" }));
    await waitFor(() => {
      expect(screen.getByLabelText("Spec document")).toHaveValue("specVersion: 1\nbridges: []\n");
    });
    await user.click(screen.getByRole("button", { name: "Plan against live state (stages a draft)" }));

    expect(await screen.findByText("unsupported specVersion 2")).toBeInTheDocument();
  });
});
