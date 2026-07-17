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
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true } },
};

const readOnlySession: MeResponse = {
  user: { username: "auditor", realm: "pve" },
  caps: { "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true } },
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
