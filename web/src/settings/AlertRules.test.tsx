// T-1005 AC5: rule CRUD form validation and delivery-log rendering against
// a mocked API — mirrors web/src/blueprints/BlueprintsPage.test.tsx's
// pattern of mocking this feature's own query-hooks module (not the raw
// fetch layer) so the component under test exercises real form/validation
// logic against controllable, synchronous mutation stand-ins.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AlertDeliveriesListResponse, AlertRule, AlertRulesListResponse, MeResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { AlertRules } from "./AlertRules";

const RULE_A: AlertRule = {
  id: "r1",
  name: "Errors to Slack",
  enabled: true,
  severityFilter: ["error"],
  targetKind: "slack",
  targetUrl: "https://hooks.slack.com/services/x",
  hasSecret: false,
  createdAt: 100,
  digestWindowSec: 0,
  bypassQuietHoursOnError: true,
  updatedAt: 100,
};

const RULE_B: AlertRule = {
  id: "r2",
  name: "Health to Gotify",
  enabled: false,
  sourceFilter: ["health"],
  targetKind: "gotify",
  targetUrl: "https://gotify.example/message",
  hasSecret: true,
  createdAt: 200,
  digestWindowSec: 0,
  bypassQuietHoursOnError: true,
  updatedAt: 200,
};

let rulesListResponse: AlertRulesListResponse = { items: [RULE_A, RULE_B] };
let deliveriesResponse: AlertDeliveriesListResponse = { items: [] };

const createMutateAsync = vi.fn();
const updateMutateAsync = vi.fn();
const deleteMutateAsync = vi.fn();
const testMutateAsync = vi.fn();

vi.mock("./alertRulesQueries", () => ({
  useAlertRulesQuery: () => ({ data: rulesListResponse, isLoading: false, error: null }),
  useAlertDeliveriesQuery: () => ({ data: deliveriesResponse }),
  useCreateAlertRuleMutation: () => ({ mutateAsync: createMutateAsync, isPending: false }),
  useUpdateAlertRuleMutation: () => ({ mutateAsync: updateMutateAsync, isPending: false }),
  useDeleteAlertRuleMutation: () => ({ mutateAsync: deleteMutateAsync, isPending: false }),
  useTestAlertRuleMutation: () => ({ mutateAsync: testMutateAsync, isPending: false }),
}));

let mockSession: MeResponse | undefined;
vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

const readOnlySession: MeResponse = {
  user: { username: "auditor", realm: "pve" },
  caps: { "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true, capture: false } },
};

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <AlertRules />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  rulesListResponse = { items: [RULE_A, RULE_B] };
  deliveriesResponse = { items: [] };
  mockSession = fullSession;
  createMutateAsync.mockReset().mockResolvedValue({ ...RULE_A, id: "new-id" });
  updateMutateAsync.mockReset().mockResolvedValue(RULE_A);
  deleteMutateAsync.mockReset().mockResolvedValue(undefined);
  testMutateAsync.mockReset().mockResolvedValue({ status: "delivered" });
});

describe("AlertRules list + selection", () => {
  it("lists every configured rule", () => {
    renderPage();
    expect(screen.getByRole("button", { name: /Errors to Slack/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Health to Gotify/ })).toBeInTheDocument();
  });

  it("selecting a rule populates the edit form with its fields", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /Errors to Slack/ }));

    expect(screen.getByLabelText("Name")).toHaveValue("Errors to Slack");
    expect(screen.getByLabelText("Target URL")).toHaveValue("https://hooks.slack.com/services/x");
    expect(screen.getByLabelText("Target kind")).toHaveValue("slack");
  });
});

describe("AlertRules form validation", () => {
  it("disables Save when name is empty", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));

    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    expect(screen.getByText("Name is required.")).toBeInTheDocument();
  });

  it("disables Save when the target URL is not an absolute http(s) URL", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));

    await user.type(screen.getByLabelText("Name"), "My rule");
    await user.type(screen.getByLabelText("Target URL"), "not-a-url");
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    expect(screen.getByText(/absolute http\(s\) URL/)).toBeInTheDocument();
  });

  it("submits a valid new rule", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));

    await user.type(screen.getByLabelText("Name"), "My rule");
    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    const saveButton = screen.getByRole("button", { name: "Create" });
    expect(saveButton).not.toBeDisabled();
    await user.click(saveButton);

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({ name: "My rule", targetUrl: "https://example.com/hook", targetKind: "generic" }),
      );
    });
  });

  it("deletes a rule and clears the selection", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /Errors to Slack/ }));
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(deleteMutateAsync).toHaveBeenCalledWith("r1");
    });
  });

  it("sends a test alert for the selected rule", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /Errors to Slack/ }));
    await user.click(screen.getByRole("button", { name: "Test" }));

    await waitFor(() => {
      expect(testMutateAsync).toHaveBeenCalledWith("r1");
    });
  });
});

describe("AlertRules read-only gating", () => {
  it("disables New rule / Save / Delete / Test for a netWrite-less session", async () => {
    mockSession = readOnlySession;
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByRole("button", { name: "New rule" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: /Errors to Slack/ }));
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Test" })).toBeDisabled();
  });
});

describe("AlertRules delivery log", () => {
  it("renders an empty state with no deliveries", () => {
    renderPage();
    expect(screen.getByText("No deliveries logged yet.")).toBeInTheDocument();
  });

  it("renders delivery rows from the mocked query", () => {
    deliveriesResponse = {
      items: [
        { id: "d1", ruleId: "r1", findingId: "f1", at: 1700000000, attempt: 1, status: "retrying", error: "http 500" },
        { id: "d2", ruleId: "r1", findingId: "f1", at: 1700000010, attempt: 2, status: "delivered" },
      ],
    };
    renderPage();
    const log = screen.getByTestId("delivery-log");
    expect(log).toHaveTextContent("retrying");
    expect(log).toHaveTextContent("delivered");
    expect(log).toHaveTextContent("http 500");
  });
});

// Debt sweep "found during the sweep, not yet carded" (2026-08-19): the
// source-filter checkbox group used to be a 5-of-17 literal array, so an
// operator could never route an alert rule on 12 of `internal/findings`'s
// 17 real sources. Mirrors the fix's own doc comment (AlertRules.tsx's
// `SOURCE_LABELS`) — every source needs a checkbox with a real label, and
// picking one of the previously-missing sources must reach the request.
describe("AlertRules source filter (debt sweep item 9 follow-up)", () => {
  it("offers a labeled checkbox for every finding source, not just the original 5", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));

    for (const label of [
      "Drift",
      "LLDP",
      "IPAM",
      "Health",
      "Verify live",
      "WireGuard",
      "WAN",
      "Flow",
      "Kubernetes",
      "Rogue",
      "Capacity",
      "Baseline",
      "Federation",
      "Peer",
      "Store",
      "Certificates",
      "Git sync",
    ]) {
      expect(screen.getByRole("checkbox", { name: label })).toBeInTheDocument();
    }
  });

  it("sends a previously-unreachable source (gitsync) in the create request", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));

    await user.type(screen.getByLabelText("Name"), "Git sync failures");
    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    await user.click(screen.getByRole("checkbox", { name: "Git sync" }));
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledWith(expect.objectContaining({ sourceFilter: ["gitsync"] }));
    });
  });
});

// T-2407: the delivery-scheduling fields. The point of these tests is that
// the fields are reachable and enforced from the UI — an operator who cannot
// set quiet hours without curl does not have quiet hours.
describe("AlertRules delivery scheduling (T-2407)", () => {
  it("populates the schedule fields from an existing rule", async () => {
    const user = userEvent.setup();
    rulesListResponse = {
      items: [
        {
          ...RULE_A,
          quietStart: "22:00",
          quietEnd: "06:00",
          quietTz: "Europe/Bucharest",
          digestWindowSec: 300,
          bypassQuietHoursOnError: false,
        },
      ],
    };
    renderPage();
    await user.click(screen.getByRole("button", { name: /Errors to Slack/ }));

    expect(screen.getByLabelText("Quiet from (HH:MM)")).toHaveValue("22:00");
    expect(screen.getByLabelText("Quiet until (HH:MM)")).toHaveValue("06:00");
    expect(screen.getByLabelText("Time zone")).toHaveValue("Europe/Bucharest");
    // Seconds on the wire, minutes in the form — 300s is 5 minutes.
    expect(screen.getByLabelText("Digest window (minutes)")).toHaveValue("5");
    expect(screen.getByRole("checkbox", { name: /error.*severity findings during quiet hours/i })).not.toBeChecked();
  });

  it("refuses a half-configured quiet-hours window", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));
    await user.type(screen.getByLabelText("Name"), "Night");
    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    await user.type(screen.getByLabelText("Quiet from (HH:MM)"), "22:00");

    expect(screen.getByText("Quiet hours needs both a start and an end.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();

    await user.type(screen.getByLabelText("Quiet until (HH:MM)"), "06:00");
    expect(screen.getByRole("button", { name: "Create" })).toBeEnabled();
  });

  it("refuses a malformed clock time rather than sending it", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));
    await user.type(screen.getByLabelText("Name"), "Night");
    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    await user.type(screen.getByLabelText("Quiet from (HH:MM)"), "10pm");
    await user.type(screen.getByLabelText("Quiet until (HH:MM)"), "06:00");

    expect(screen.getByText("Quiet hours must be HH:MM (24-hour).")).toBeInTheDocument();
    expect(createMutateAsync).not.toHaveBeenCalled();
  });

  it("sends the schedule with the create request, converting minutes to seconds", async () => {
    const user = userEvent.setup();
    createMutateAsync.mockResolvedValue({ ...RULE_A, id: "new" });
    renderPage();
    await user.click(screen.getByRole("button", { name: "New rule" }));
    await user.type(screen.getByLabelText("Name"), "Night");
    await user.type(screen.getByLabelText("Target URL"), "https://example.com/hook");
    await user.type(screen.getByLabelText("Quiet from (HH:MM)"), "22:00");
    await user.type(screen.getByLabelText("Quiet until (HH:MM)"), "06:00");
    await user.type(screen.getByLabelText("Digest window (minutes)"), "10");
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalled();
    });
    const req: unknown = createMutateAsync.mock.calls[0]?.[0];
    expect(req).toMatchObject({
      quietStart: "22:00",
      quietEnd: "06:00",
      digestWindowSec: 600,
      bypassQuietHoursOnError: true,
    });
  });
});
