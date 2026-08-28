// SPDX-License-Identifier: Apache-2.0

// T-4006 acceptance criterion 3: the change-calendar view renders at least
// one declared freeze window and one scheduled changeset on the same
// timeline.
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CalendarView } from "../api/policies";
import { CalendarPanel } from "./CalendarPanel";

const fetchCalendar = vi.fn<() => Promise<CalendarView>>();

vi.mock("../api/policies", async () => {
  const actual = await vi.importActual<typeof import("../api/policies")>("../api/policies");
  return { ...actual, fetchCalendar: () => fetchCalendar() };
});

function renderPanel(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <CalendarPanel />
    </QueryClientProvider> as ReactNode,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("CalendarPanel (T-4006 AC3)", () => {
  it("renders a declared freeze window and a scheduled changeset on the same screen", async () => {
    fetchCalendar.mockResolvedValue({
      freezeWindows: [
        {
          ruleId: "friday-afternoon-freeze",
          description: "No changes during the Friday afternoon freeze.",
          severity: "deny",
          zone: "America/New_York",
          recognized: true,
          weekdays: ["fri"],
          minuteOfDayStart: 840,
          minuteOfDayEnd: 1080,
        },
      ],
      schedules: [
        {
          changesetId: "cs-1",
          windowStart: 1_700_000_000,
          windowEnd: 1_700_000_100,
          confirmTimeoutSec: 30,
          missedWindowPolicy: "skip",
          status: "pending",
          createdBy: "alice",
          createdAt: 1_699_999_000,
        },
      ],
    });

    renderPanel();

    const freezeRow = await screen.findByTestId("freeze-window-row");
    expect(freezeRow).toHaveTextContent("friday-afternoon-freeze");
    expect(freezeRow).toHaveTextContent(/deny/i);

    const scheduleRow = await screen.findByTestId("pending-schedule-row");
    expect(scheduleRow).toHaveTextContent("cs-1");
  });

  it("renders an unrecognized window by description, without inventing a time box", async () => {
    fetchCalendar.mockResolvedValue({
      freezeWindows: [
        { ruleId: "custom-freeze", description: "A custom, irregular freeze.", severity: "warn", recognized: false },
      ],
      schedules: null,
    });

    renderPanel();

    const freezeRow = await screen.findByTestId("freeze-window-row");
    expect(freezeRow).toHaveTextContent("A custom, irregular freeze.");
    expect(freezeRow).toHaveTextContent(/too irregular/i);
    expect(screen.getByText(/nothing is currently scheduled/i)).toBeInTheDocument();
  });

  it("shows nothing declared when the policy set has no freeze windows", async () => {
    fetchCalendar.mockResolvedValue({ freezeWindows: [], schedules: [] });
    renderPanel();
    expect(await screen.findByText(/none declared/i)).toBeInTheDocument();
  });
});
