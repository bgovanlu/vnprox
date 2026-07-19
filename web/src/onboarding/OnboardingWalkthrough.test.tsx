// Component-level tests for the onboarding walkthrough: every API call is
// mocked at its api/*.ts boundary (same convention as
// changesets/ChangesetDrawer.test.tsx), so these run entirely against fake
// data. Covers T-605 AC1/AC2's step-through, skip, dismiss/resume, and the
// step-2 pre-fill-from-suggestion behavior against a suggest response
// shaped like testdata/clusters/three-node-vlan.yaml's real detection
// output (see protectedDraft.test.ts for the equivalent pure-function
// coverage of that same fixture shape).
//
// Each test drives the starting step via the mocked GET /layouts/onboarding
// response (`mockOnboardingProgress`, read by the mock below) rather than
// poking the TanStack Query cache post-render — the progress query has no
// `enabled` gate (unlike ChangesetDrawer's per-id changeset query), so a
// setQueryData call racing an already-in-flight initial fetch would be
// flaky. Only the dedicated 404 test exercises the "fresh install" fallback
// path via a one-off mockRejectedValueOnce.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type {
  DriftFinding,
  LldpResponse,
  MeResponse,
  OnboardingProgress,
  ProtectedInterfacesResponse,
  ProtectedInterfacesSuggestResponse,
  TopologyResponse,
} from "../api/types";
import { ApiError } from "../api/client";
import { OnboardingWalkthrough } from "./OnboardingWalkthrough";
import { freshOnboardingProgress } from "./onboardingMachine";

vi.mock("../api/onboarding", () => ({
  fetchOnboardingProgress: vi.fn(() =>
    Promise.resolve({ name: "onboarding", layout: mockOnboardingProgress, updatedAt: 0 }),
  ),
  saveOnboardingProgress: vi.fn((progress: OnboardingProgress) =>
    Promise.resolve({ name: "onboarding", layout: progress, updatedAt: 0 }),
  ),
}));

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

vi.mock("../api/topology", () => ({
  fetchTopology: vi.fn(() => Promise.resolve(mockTopology)),
  fetchInventoryDetail: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/drift", () => ({
  fetchDrift: vi.fn(() => Promise.resolve(mockDrift)),
  fixDriftFinding: vi.fn(),
}));

vi.mock("../api/protectedInterfaces", () => ({
  fetchProtectedInterfaces: vi.fn(() => Promise.resolve(mockExistingProtected)),
  fetchProtectedInterfacesSuggest: vi.fn(() => Promise.resolve(mockSuggestion)),
  saveProtectedInterfaces: vi.fn((req: { nodes: Record<string, string[]> }) =>
    Promise.resolve({ nodes: req.nodes, updatedAt: 1, version: 1 }),
  ),
}));

vi.mock("../api/lldp", () => ({
  fetchLldpNeighbors: vi.fn(() => Promise.resolve(mockLldp)),
  installLldp: vi.fn(() => Promise.resolve({ results: [{ node: "pve1", ok: true }] })),
}));

import * as onboardingApi from "../api/onboarding";
import * as protectedApi from "../api/protectedInterfaces";

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

const THREE_NODE_VLAN_SUGGESTION: ProtectedInterfacesSuggestResponse = {
  nodes: { pve1: ["bridge:pve1:vmbr0"], pve2: ["bridge:pve2:vmbr0"], pve3: ["bridge:pve3:vmbr0"] },
};

let mockOnboardingProgress: OnboardingProgress = freshOnboardingProgress();
let mockSession: MeResponse | undefined = fullSession;
let mockTopology: TopologyResponse = { nodes: [], edges: [], layers: ["phys", "l2", "sdn", "guest"], generatedAt: 0 };
let mockDrift: DriftFinding[] = [];
let mockLldp: LldpResponse = { items: [] };
let mockSuggestion: ProtectedInterfacesSuggestResponse = THREE_NODE_VLAN_SUGGESTION;
let mockExistingProtected: ProtectedInterfacesResponse = { nodes: {}, updatedAt: 0, version: 0 };

function atStep(step: OnboardingProgress["currentStep"]): OnboardingProgress {
  return { ...freshOnboardingProgress(), currentStep: step };
}

function renderWalkthrough(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <OnboardingWalkthrough />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("OnboardingWalkthrough", () => {
  afterEach(() => {
    vi.clearAllMocks();
    mockOnboardingProgress = freshOnboardingProgress();
    mockSession = fullSession;
    mockTopology = { nodes: [], edges: [], layers: ["phys", "l2", "sdn", "guest"], generatedAt: 0 };
    mockDrift = [];
    mockLldp = { items: [] };
    mockSuggestion = THREE_NODE_VLAN_SUGGESTION;
    mockExistingProtected = { nodes: {}, updatedAt: 0, version: 0 };
  });

  it("starts at step 1 (found-summary) on a fresh install (404 from GET /layouts/onboarding)", async () => {
    vi.mocked(onboardingApi.fetchOnboardingProgress).mockRejectedValueOnce(
      new ApiError(404, "not_found", "no saved progress"),
    );
    renderWalkthrough();
    expect(await screen.findByRole("region", { name: "Onboarding walkthrough" })).toBeInTheDocument();
    expect(screen.getByText(/1\/4/)).toBeInTheDocument();
    expect(screen.getByText("What we found")).toBeInTheDocument();
  });

  it("advances found-summary -> protected on Continue, persisting the transition", async () => {
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("What we found");

    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(onboardingApi.saveOnboardingProgress).toHaveBeenCalledWith(
        expect.objectContaining({ currentStep: "protected", completedSteps: ["found-summary"] }),
      );
    });
    expect(await screen.findByText("Protected interfaces")).toBeInTheDocument();
  });

  it("step 2 pre-fills checkboxes from GET /protected-interfaces/suggest shaped like three-node-vlan.yaml", async () => {
    mockOnboardingProgress = atStep("protected");
    renderWalkthrough();

    await screen.findByText("Protected interfaces");
    const checkbox = await screen.findByRole("checkbox", { name: /bridge:pve1:vmbr0/ });
    expect(checkbox).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /bridge:pve2:vmbr0/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /bridge:pve3:vmbr0/ })).toBeChecked();
    expect(screen.getByText("3 interface(s) selected.")).toBeInTheDocument();
  });

  it("unchecking a suggested ref then confirming saves it excluded, and advances to the lldp step", async () => {
    mockOnboardingProgress = atStep("protected");
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("Protected interfaces");

    await user.click(await screen.findByRole("checkbox", { name: /bridge:pve2:vmbr0/ }));
    expect(screen.getByText("2 interface(s) selected.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Confirm protected interfaces" }));

    await waitFor(() => {
      // TanStack Query v5's mutationFn is invoked with a second (internal
      // context) argument alongside the variables this app passes —
      // matched loosely here since only the request payload matters.
      expect(protectedApi.saveProtectedInterfaces).toHaveBeenCalledWith(
        { nodes: { pve1: ["bridge:pve1:vmbr0"], pve3: ["bridge:pve3:vmbr0"] } },
        expect.anything(),
      );
    });
    expect(await screen.findByText("Physical discovery")).toBeInTheDocument();
  });

  it("skipping the protected step advances without writing, and records it as skipped", async () => {
    mockOnboardingProgress = atStep("protected");
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("Protected interfaces");

    await user.click(screen.getByRole("button", { name: "Skip" }));

    expect(protectedApi.saveProtectedInterfaces).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(onboardingApi.saveOnboardingProgress).toHaveBeenCalledWith(
        expect.objectContaining({ currentStep: "lldp", skippedSteps: ["protected"] }),
      );
    });
  });

  it("disables the confirm button with a tooltip reason for a netWrite-less (read-only) session", async () => {
    mockOnboardingProgress = atStep("protected");
    mockSession = {
      user: { username: "auditor", realm: "pve" },
      caps: { "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true, capture: false } },
    };
    renderWalkthrough();
    await screen.findByText("Protected interfaces");

    expect(await screen.findByRole("button", { name: "Confirm protected interfaces" })).toBeDisabled();
    // The step is still visible and skippable for a read-only session (T-605
    // brief: "the walkthrough should still let a read-only user view/skip
    // these steps", never hidden).
    expect(screen.getByRole("button", { name: "Skip" })).toBeEnabled();
  });

  it("lldp step offers 'Enable LLDP discovery' when GET /lldp returns no items, and advances on success", async () => {
    mockOnboardingProgress = atStep("lldp");
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("Physical discovery");
    expect(await screen.findByText(/No LLDP neighbors seen yet/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Enable LLDP discovery" }));

    await waitFor(() => {
      expect(screen.getByText("Health findings")).toBeInTheDocument();
    });
  });

  it("lldp step shows 'Continue' (no install offered) when neighbors are already reporting", async () => {
    mockOnboardingProgress = atStep("lldp");
    mockLldp = {
      items: [{ ref: "lldp:pve1:eno1", node: "pve1", localIface: "eno1", protocol: "lldp", chassisName: "sw1", chassisId: "sw1", portId: "1" }],
    };
    renderWalkthrough();
    await screen.findByText("Physical discovery");
    expect(await screen.findByText(/already reporting/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Enable LLDP discovery" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
  });

  it("health step summarizes GET /drift as a count + severity breakdown and finishing reaches 'done'", async () => {
    mockOnboardingProgress = atStep("health");
    mockDrift = [
      { id: "1", check: "mtu_consistency", severity: "error", detail: "x", nodes: ["pve1"], fixable: false },
      { id: "2", check: "mtu_consistency", severity: "warning", detail: "y", nodes: ["pve1"], fixable: false },
    ];
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("Health findings");
    // The count is a bolded <span> nested inside the <p>, so the default
    // text matcher (which only concatenates an element's own direct text
    // nodes, not descendant elements') can't match the whole sentence in
    // one go — match on the <p>'s full textContent instead.
    await waitFor(() => {
      const summary = screen.getByText(
        (_, element) => (element?.textContent ?? "").includes("2 finding(s) (1 error, 1 warning, 0 infos)"),
        { selector: "p" },
      );
      expect(summary).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: "Finish" }));

    await waitFor(() => {
      expect(onboardingApi.saveOnboardingProgress).toHaveBeenCalledWith(expect.objectContaining({ currentStep: "done" }));
    });
  });

  it("dismissing shows the reopen pill, and resuming brings the panel back at the same step", async () => {
    mockOnboardingProgress = atStep("lldp");
    const user = userEvent.setup();
    renderWalkthrough();
    await screen.findByText("Physical discovery");

    await user.click(screen.getByRole("button", { name: "Minimize onboarding walkthrough" }));

    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Onboarding walkthrough" })).not.toBeInTheDocument();
    });
    const pill = await screen.findByRole("button", { name: /Resume setup walkthrough/ });
    expect(pill).toHaveTextContent("3/4");

    await user.click(pill);

    expect(await screen.findByRole("region", { name: "Onboarding walkthrough" })).toBeInTheDocument();
    expect(screen.getByText("Physical discovery")).toBeInTheDocument();
  });

  it("renders nothing once the walkthrough is done (not even the reopen pill)", async () => {
    mockOnboardingProgress = atStep("done");
    renderWalkthrough();

    await waitFor(() => {
      expect(onboardingApi.fetchOnboardingProgress).toHaveBeenCalled();
    });
    expect(screen.queryByRole("region", { name: "Onboarding walkthrough" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Resume setup walkthrough/ })).not.toBeInTheDocument();
  });
});
