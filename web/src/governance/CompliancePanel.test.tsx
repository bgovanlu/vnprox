// SPDX-License-Identifier: Apache-2.0

// T-3002 AC4, on the rendered DOM: a compliance control with no mapped
// evidence renders as `unmapped` — never as pass, and never hidden.
import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ComplianceProfileSummary, ComplianceReport } from "../api/compliance";
import { CompliancePanel } from "./CompliancePanel";

const fetchComplianceProfiles = vi.fn<() => Promise<{ items: ComplianceProfileSummary[] }>>();
const fetchComplianceReport = vi.fn<() => Promise<ComplianceReport>>();

vi.mock("../api/compliance", async () => {
  const actual = await vi.importActual<typeof import("../api/compliance")>("../api/compliance");
  return {
    ...actual,
    fetchComplianceProfiles: () => fetchComplianceProfiles(),
    fetchComplianceReport: () => fetchComplianceReport(),
  };
});

const profile: ComplianceProfileSummary = {
  id: "general-network-hygiene",
  title: "General network hygiene",
  version: "1",
  notice: "This report asserts compliance with no published framework.",
  controlCount: 3,
  mappedChecks: 2,
  unmappedControls: 1,
};

const report: ComplianceReport = {
  productVersion: "3.0.3",
  profileId: "general-network-hygiene",
  profileTitle: "General network hygiene",
  profileVersion: "1",
  notice: "This report asserts compliance with no published framework.",
  generatedAt: 1_700_000_000,
  summary: { pass: 1, fail: 0, notEvaluated: 1, unmapped: 1, total: 3 },
  checkUniverse: "every check this build can emit",
  controls: [
    {
      id: "NET-1",
      title: "Management path redundancy",
      statement: "Every node reaches the cluster over more than one path.",
      status: "pass",
      evidence: [{ kind: "check", name: "mgmt_single_path", status: "satisfied", detail: "no open finding" }],
    },
    {
      id: "NET-2",
      title: "Segmentation posture",
      statement: "Guest traffic is segmented.",
      status: "not_evaluated",
      evidence: [{ kind: "posture", name: "segmentation", status: "not_evaluated", detail: "posture could not assess it" }],
    },
    {
      id: "ORG-1",
      title: "Change approval records are retained off-cluster",
      statement: "Approval records survive the cluster they describe.",
      status: "unmapped",
      unmappedReason: "vnprox observes nothing about off-cluster retention.",
    },
  ],
  unmappedChecks: ["capacity_link_forecast"],
};

function renderPanel(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <CompliancePanel />
    </QueryClientProvider> as ReactNode,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchComplianceProfiles.mockResolvedValue({ items: [profile] });
  fetchComplianceReport.mockResolvedValue(report);
});

describe("CompliancePanel (T-3002 AC4)", () => {
  it("renders an unmapped control as unmapped — visible, and not a pass", async () => {
    renderPanel();
    const control = await screen.findByTestId("compliance-control-ORG-1");

    // Hidden would be the other way this fails: an operator who never sees the
    // control cannot tell it from one that passed.
    expect(control).toBeInTheDocument();
    expect(control).toHaveAttribute("data-status", "unmapped");
    expect(control).toHaveTextContent(/unmapped/i);
    expect(control).not.toHaveTextContent(/\bpass\b/);
    // The profile's stated reason, verbatim.
    expect(within(control).getByTestId("compliance-unmapped-ORG-1")).toHaveTextContent(
      "vnprox observes nothing about off-cluster retention.",
    );
  });

  it("does not fold not_evaluated into either neighbour", async () => {
    renderPanel();
    const control = await screen.findByTestId("compliance-control-NET-2");
    expect(control).toHaveAttribute("data-status", "not_evaluated");
    expect(control).toHaveTextContent(/absence of evidence is not evidence of compliance/i);
  });

  it("counts one pass out of three controls", async () => {
    renderPanel();
    const summary = await screen.findByTestId("compliance-summary");
    // pass / fail / not evaluated / unmapped, in that order.
    expect(summary).toHaveTextContent(/pass\s*1/);
    expect(summary).toHaveTextContent(/unmapped\s*1/);
    expect(screen.queryByTestId("compliance-summary-drift")).toBeNull();
  });

  it("shows an unrecognised status as unrecognised and flags the summary mismatch", async () => {
    fetchComplianceReport.mockResolvedValue({
      ...report,
      controls: [...report.controls, { id: "NEW-1", title: "future", statement: "", status: "partially_compliant" }],
    });
    renderPanel();
    const control = await screen.findByTestId("compliance-control-NEW-1");
    expect(control).toHaveAttribute("data-status", "unknown");
    expect(control).toHaveTextContent("partially_compliant");
    expect(control).toHaveTextContent(/not being counted as a pass/i);
    expect(await screen.findByTestId("compliance-summary-drift")).toBeInTheDocument();
  });

  it("reports a failed report as unknown rather than as a clean sheet", async () => {
    fetchComplianceReport.mockRejectedValue(new Error("outside the retention window"));
    renderPanel();
    const err = await screen.findByTestId("compliance-report-error");
    expect(err).toHaveTextContent("outside the retention window");
    expect(err).toHaveTextContent(/none of them may be read as passing/i);
    expect(screen.queryByTestId("compliance-control-NET-1")).toBeNull();
  });
});
