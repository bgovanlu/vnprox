// T-1507: MigrationPreflightTab.test.tsx covers the target-node input,
// triggering the pre-flight check, and rendering the returned verdict/
// headroom/caveats against a mocked API response — mirrors
// InteriorTab.test.tsx's mock-the-api-module shape.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { MigrationAssessment } from "../api/types";
import { MigrationPreflightTab } from "./MigrationPreflightTab";

const REF = "guest:pve1:100";

let nextResult: MigrationAssessment;
const postMigrationPreflight = vi.fn((_req: { guest: string; targetNode: string }) => Promise.resolve(nextResult));

vi.mock("../api/migration", () => ({
  postMigrationPreflight: (req: { guest: string; targetNode: string }) => postMigrationPreflight(req),
}));

function renderTab() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MigrationPreflightTab entityRef={REF} />
    </QueryClientProvider>,
  );
}

describe("MigrationPreflightTab", () => {
  it("disables the check button until a target node is entered, then runs the check", async () => {
    nextResult = {
      headroomMbps: 900,
      estimatedTransferSec: 18.2,
      verdict: "ok",
      bestEffort: true,
      caveats: [],
    };
    const user = userEvent.setup();
    renderTab();

    const button = screen.getByRole("button", { name: /check headroom/i });
    expect(button).toBeDisabled();

    await user.type(screen.getByLabelText(/target node/i), "pve2");
    expect(button).not.toBeDisabled();

    await user.click(button);

    await waitFor(() => {
      expect(postMigrationPreflight).toHaveBeenCalledWith({ guest: REF, targetNode: "pve2" });
    });

    await screen.findByText(/OK — ample headroom/);
    expect(screen.getByTestId("migration-preflight-verdict")).toHaveTextContent("Headroom: 900 Mbps");
    expect(screen.getByText(/no live guest instrumentation/i)).toBeInTheDocument();
  });

  it("renders caveats and an insufficient verdict", async () => {
    nextResult = {
      headroomMbps: 0,
      estimatedTransferSec: -1,
      verdict: "insufficient",
      bestEffort: true,
      caveats: ["no bandwidth headroom remains on this link after accounting for current migration traffic"],
    };
    const user = userEvent.setup();
    renderTab();

    await user.type(screen.getByLabelText(/target node/i), "pve3");
    await user.click(screen.getByRole("button", { name: /check headroom/i }));

    await screen.findByText(/Insufficient/);
    expect(screen.getByTestId("migration-preflight-verdict")).toHaveTextContent(
      "Estimated transfer: unknown (no headroom to estimate from)",
    );
    expect(screen.getByTestId("migration-preflight-caveats")).toHaveTextContent(
      "no bandwidth headroom remains on this link",
    );
  });
});
