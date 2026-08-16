// T-3002 AC5: the digest schedule round-trips through PUT and reflects the
// daemon's stored value afterwards — plus the `everySec: 0` honesty the
// contract is explicit about.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { DigestSchedule, DigestScheduleUpdate } from "../api/digest";
import { DigestSchedulePanel } from "./DigestSchedulePanel";

const fetchDigestSchedule = vi.fn<() => Promise<DigestSchedule>>();
const putDigestSchedule = vi.fn<(u: DigestScheduleUpdate) => Promise<DigestSchedule>>();

vi.mock("../api/digest", async () => {
  const actual = await vi.importActual<typeof import("../api/digest")>("../api/digest");
  return {
    ...actual,
    fetchDigestSchedule: () => fetchDigestSchedule(),
    putDigestSchedule: (u: DigestScheduleUpdate) => putDigestSchedule(u),
  };
});

/** The shape a daemon that has never had a schedule written answers with. */
const neverWritten: DigestSchedule = {
  enabled: false,
  everySec: 0,
  ruleIds: [],
  updatedBy: "",
  updatedAt: 0,
  lastRun: null,
};

function renderPanel(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <DigestSchedulePanel />
    </QueryClientProvider> as ReactNode,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchDigestSchedule.mockResolvedValue(neverWritten);
});

describe("DigestSchedulePanel (T-3002 AC5)", () => {
  it("round-trips through PUT and re-renders the daemon's stored value", async () => {
    const user = userEvent.setup();
    const stored: DigestSchedule = {
      enabled: true,
      everySec: 604800,
      ruleIds: ["rule-a", "rule-b"],
      updatedBy: "brian@pam",
      updatedAt: 1_700_000_000,
      lastRun: null,
    };
    // The daemon really stores it: a later GET answers the new value. Without
    // this the round trip is only half-modelled, and the panel's invalidate
    // would read back the old schedule.
    putDigestSchedule.mockImplementation(() => {
      fetchDigestSchedule.mockResolvedValue(stored);
      return Promise.resolve(stored);
    });

    renderPanel();
    await screen.findByTestId("digest-stored");

    await user.click(screen.getByRole("checkbox", { name: /send the digest on this cadence/i }));
    await user.click(screen.getByRole("button", { name: /^Weekly$/ }));
    await user.type(screen.getByLabelText(/digest alert rule ids/i), "rule-a, rule-b");
    await user.click(screen.getByRole("button", { name: /save schedule/i }));

    // The whole object goes back, which is the full-replace contract.
    await waitFor(() => {
      expect(putDigestSchedule).toHaveBeenCalledWith({
        enabled: true,
        everySec: 604800,
        ruleIds: ["rule-a", "rule-b"],
      });
    });

    // And the panel re-renders what the daemon stored, not what was typed.
    await waitFor(() => {
      expect(screen.getByTestId("digest-stored")).toHaveTextContent("Weekly (604800s)");
    });
    expect(screen.getByTestId("digest-stored")).toHaveTextContent("brian@pam");
    expect(screen.getByTestId("digest-stored")).toHaveTextContent(/Filtered to 2 alert rules/);
  });

  it("never renders a stored cadence of 0 as a cadence", async () => {
    renderPanel();
    const stored = await screen.findByTestId("digest-stored");
    expect(stored).toHaveTextContent(/No cadence has ever been stored/);
    expect(stored).not.toHaveTextContent(/Weekly/);
    expect(stored).not.toHaveTextContent(/every 0 seconds/i);
    // An empty rule filter is "every rule", not "no recipients".
    expect(stored).toHaveTextContent(/every alert rule's targets/i);
    expect(screen.getByTestId("digest-last-run")).toHaveTextContent(/No tick has run yet/);
  });

  it("refuses to send an enabled schedule below the server's floor", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByTestId("digest-stored");

    await user.click(screen.getByRole("checkbox", { name: /send the digest on this cadence/i }));
    await user.type(screen.getByLabelText(/digest cadence seconds/i), "60");

    expect(await screen.findByTestId("digest-cadence-error")).toHaveTextContent(/at least 3600/);
    expect(screen.getByRole("button", { name: /save schedule/i })).toBeDisabled();
    expect(putDigestSchedule).not.toHaveBeenCalled();
  });

  it("distinguishes a deployment without digests from an empty schedule", async () => {
    fetchDigestSchedule.mockRejectedValue(
      new ApiError(501, "not_implemented", "scheduled digests are not available on this deployment"),
    );
    renderPanel();
    const notice = await screen.findByTestId("digest-unavailable");
    expect(notice).toHaveTextContent(/not available on this deployment/);
    expect(notice).toHaveTextContent(/property of the daemon, not an empty schedule/i);
    // No form is offered for something that cannot be scheduled.
    expect(screen.queryByRole("button", { name: /save schedule/i })).toBeNull();
  });
});
