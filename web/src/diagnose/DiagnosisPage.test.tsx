// T-1307 AC6's Vitest coverage: the step-by-step ladder result renders
// (statuses, per-step summaries, expandable detail), the verdict renders
// with its confidence, and a `suggestedFixRef` "Review suggested fix"
// button wires through to the same POST /findings/{id}/fix flow
// FindingsStreamPanel's own fix button uses (setActiveId opens the
// changeset drawer). The backend is mocked at the api/diagnose.ts and
// api/findings.ts boundaries, mirroring FindingsStreamPanel.test.tsx's own
// pattern.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import { useChangesetDrawerStore } from "../changesets/store";
import type { DiagnoseRequest } from "../api/diagnose";
import type { Changeset, DiagnoseResult } from "../api/types";
import { DiagnosisPage } from "./DiagnosisPage";

const TARGET_REF = "guest-nic:pve1:300/net0";

let diagnoseResult: DiagnoseResult;

const postDiagnose = vi.fn((_req: DiagnoseRequest) => Promise.resolve(diagnoseResult));
const fixFinding = vi.fn(
  (_id: string): Promise<Changeset> =>
    Promise.resolve({
      id: "cs1", title: "fix it", author: "root@pam", status: "draft", ops: [], findings: [],
      createdAt: 0, updatedAt: 0,
    }),
);

vi.mock("../api/diagnose", () => ({
  postDiagnose: (req: DiagnoseRequest) => postDiagnose(req),
}));

vi.mock("../api/findings", () => ({
  fixFinding: (id: string) => fixFinding(id),
}));

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[`/diagnose?ref=${encodeURIComponent(TARGET_REF)}`]}>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <DiagnosisPage />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function baseResult(overrides: Partial<DiagnoseResult> = {}): DiagnoseResult {
  return {
    target: TARGET_REF,
    steps: [
      { name: "config-check", status: "ran", summary: "simulated path to gateway 10.20.0.1: allow", detail: { verdict: "allow" }, ranAt: 1000 },
      { name: "live-probe", status: "ran", summary: "live probe to gateway 10.20.0.1: reachable (simulated: allow)", detail: { diverges: false }, ranAt: 1000 },
      { name: "guest-interior", status: "skipped", summary: "the guest interior inspector is not opted in for this guest", ranAt: 1000 },
      { name: "conntrack", status: "ran", summary: "2 connection(s) for guest guest:pve1:300", detail: { items: [] }, ranAt: 1000 },
      { name: "capture", status: "skipped", summary: "capture escalation was not requested for this ladder run", ranAt: 1000 },
    ],
    verdict: { summary: "3 of 5 step(s) ran; no related findings surfaced", confidence: "medium", linkedFindingIds: [] },
    ...overrides,
  };
}

describe("DiagnosisPage", () => {
  it("shows an empty state and never calls postDiagnose when no target ref is present", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/diagnose"]}>
        <QueryClientProvider client={queryClient}>
          <ToastProvider>
            <DiagnosisPage />
          </ToastProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(screen.getByText("No target selected")).toBeInTheDocument();
    expect(postDiagnose).not.toHaveBeenCalled();
  });

  it("runs the ladder and renders every step's status and summary", async () => {
    diagnoseResult = baseResult();
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: "Run diagnosis" }));

    await waitFor(() => {
      expect(postDiagnose).toHaveBeenCalledWith({ targetRef: TARGET_REF, escalateToCapture: false });
    });

    const steps = await screen.findByTestId("diagnose-steps");
    const configCheck = within(steps).getByTestId("diagnose-step-config-check");
    expect(configCheck).toHaveAttribute("data-status", "ran");
    expect(within(configCheck).getByText(/simulated path to gateway 10\.20\.0\.1: allow/)).toBeInTheDocument();

    const guestInterior = within(steps).getByTestId("diagnose-step-guest-interior");
    expect(guestInterior).toHaveAttribute("data-status", "skipped");
    expect(within(guestInterior).getByText("the guest interior inspector is not opted in for this guest")).toBeInTheDocument();

    const capture = within(steps).getByTestId("diagnose-step-capture");
    expect(capture).toHaveAttribute("data-status", "skipped");

    const verdict = screen.getByTestId("diagnose-verdict");
    expect(within(verdict).getByText(/3 of 5 step\(s\) ran/)).toBeInTheDocument();
    expect(within(verdict).getByText(/Confidence: medium/)).toBeInTheDocument();
    // No suggestedFixRef in this fixture -> no fix button.
    expect(within(verdict).queryByRole("button", { name: /Review suggested fix/ })).not.toBeInTheDocument();
  });

  it("sends escalateToCapture: true only when the checkbox is checked", async () => {
    diagnoseResult = baseResult();
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("checkbox", { name: "Escalate to packet capture" }));
    await user.click(screen.getByRole("button", { name: "Run diagnosis" }));

    await waitFor(() => {
      expect(postDiagnose).toHaveBeenCalledWith({ targetRef: TARGET_REF, escalateToCapture: true });
    });
  });

  it("expands a ran step's detail on click, and a skipped step has no detail toggle", async () => {
    diagnoseResult = baseResult();
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Run diagnosis" }));

    const steps = await screen.findByTestId("diagnose-steps");
    const configCheck = within(steps).getByTestId("diagnose-step-config-check");
    expect(within(configCheck).queryByText(/"verdict": "allow"/)).not.toBeInTheDocument();
    await user.click(within(configCheck).getByRole("button", { name: /Show detail/ }));
    expect(within(configCheck).getByText(/"verdict": "allow"/)).toBeInTheDocument();

    const guestInterior = within(steps).getByTestId("diagnose-step-guest-interior");
    expect(within(guestInterior).queryByText(/Show detail/)).not.toBeInTheDocument();
  });

  it("a suggestedFixRef renders a fix button that creates a changeset and opens the drawer", async () => {
    diagnoseResult = baseResult({
      verdict: {
        summary: "3 of 5 step(s) ran; 1 related finding(s) surfaced",
        confidence: "high",
        linkedFindingIds: ["health:bondslave|bridge:pve1:vmbr0"],
        suggestedFixRef: "health:bondslave|bridge:pve1:vmbr0",
      },
    });
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Run diagnosis" }));

    const fixButton = await screen.findByRole("button", { name: "Review suggested fix" });
    await user.click(fixButton);

    await waitFor(() => {
      expect(fixFinding).toHaveBeenCalledWith("health:bondslave|bridge:pve1:vmbr0");
    });
    await waitFor(() => {
      expect(useChangesetDrawerStore.getState().activeId).toBe("cs1");
    });
  });
});
