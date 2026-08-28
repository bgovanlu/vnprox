// SPDX-License-Identifier: Apache-2.0

// T-3002 AC1 and AC2, asserted on the rendered review screen.
//
// AC1 — a changeset blocked by a `deny` policy shows the rule id AND its
//       assertions, and the copy does not invent a reason the daemon did not
//       give.
// AC2 — break-glass requires a typed reason, states the audit consequence
//       before confirming, and is unreachable in one click from the blocked
//       state.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { ApprovalState, Changeset } from "../api/types";
import type { PolicyResult, PolicyStatus } from "../api/policies";
import { ReviewApplyScreen } from "./ReviewApplyScreen";

const fetchPolicies = vi.fn<() => Promise<PolicyStatus>>();
const testPolicy = vi.fn<() => Promise<PolicyResult>>();
const invokeBreakGlass = vi.fn<(id: string, reason: string) => Promise<unknown>>();

vi.mock("../api/changesets", () => ({
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn(() => Promise.resolve(baseChangeset())),
  validateChangeset: vi.fn(() => Promise.resolve(baseChangeset())),
  changesetImpact: vi.fn(() => Promise.resolve({ summary: "", entities: [] })),
}));

vi.mock("../api/policies", async () => {
  const actual = await vi.importActual<typeof import("../api/policies")>("../api/policies");
  return { ...actual, fetchPolicies: () => fetchPolicies(), testPolicy: () => testPolicy() };
});

vi.mock("./breakGlass", async () => {
  const actual = await vi.importActual<typeof import("./breakGlass")>("./breakGlass");
  return { ...actual, invokeBreakGlass: (id: string, reason: string) => invokeBreakGlass(id, reason) };
});

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

function baseChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "cs-gov",
    title: "Attach vm101 to vmbr0",
    author: "root@pam",
    status: "validated",
    ops: [{ op: "guest.nic.update", target: "guest:pve1:101", params: { bridge: "vmbr0" } }],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

const installedSet: PolicyStatus = {
  revision: 7,
  set: {
    version: 1,
    rules: [
      {
        id: "no-flat-vlan",
        description: "guest NICs must carry a VLAN tag",
        severity: "deny",
        match: [{ field: "op", op: "eq", value: "guest.nic.update" }],
        assert: [{ field: "params.vlan", op: "exists" }],
      },
    ],
  },
};

const denyResult: PolicyResult = {
  rules: [
    {
      ruleId: "no-flat-vlan",
      description: "guest NICs must carry a VLAN tag",
      severity: "deny",
      matchedOps: [0],
      violatingOps: [0],
    },
  ],
};

/** The approval state of a changeset in a protected op class with nobody's
 * approval on it — the state break-glass exists for. */
function twoPersonBlocked(): ApprovalState {
  return {
    status: "none",
    required: false,
    twoPerson: {
      required: 2,
      satisfied: false,
      classes: [{ class: "guest.*", approvals: 2, ops: 1 }],
      approvers: [],
    },
  };
}

function renderReview(cs: Changeset): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ReviewApplyScreen changeset={cs} onClose={() => undefined} />
      </ToastProvider>
    </QueryClientProvider> as ReactNode,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchPolicies.mockResolvedValue(installedSet);
  testPolicy.mockResolvedValue({});
  invokeBreakGlass.mockResolvedValue({
    changesetId: "cs-gov",
    reason: "r",
    invokedBy: "root@pam",
    invokedAt: 1,
    ackableAt: 2,
  });
});

describe("ReviewApplyScreen — policy deny verdict (T-3002 AC1)", () => {
  it("names the denying rule id and its assertions, not just the generic error", async () => {
    testPolicy.mockResolvedValue(denyResult);
    renderReview(
      baseChangeset({
        findings: [
          {
            severity: "error",
            code: "policy.violation",
            message:
              'policy rule "no-flat-vlan": guest NICs must carry a VLAN tag (op guest.nic.update: failed assertion params.vlan exists)',
            ref: "guest:pve1:101",
          },
        ],
      }),
    );

    const panel = await screen.findByRole("region", { name: /policy verdict/i });
    await within(panel).findByTestId("policy-rule-no-flat-vlan");

    const card = within(panel).getByTestId("policy-rule-no-flat-vlan");
    // The rule id, its severity and the assertion that had to hold.
    expect(card).toHaveTextContent("no-flat-vlan");
    expect(card).toHaveTextContent(/deny — blocks apply/);
    expect(card).toHaveTextContent("params.vlan exists");
    expect(within(panel).getByText(/refused by the cluster's installed policy/i)).toBeInTheDocument();

    // AC1's second half: it does not invent a reason. The engine's own
    // message is reproduced verbatim, and no assertion is claimed that the
    // installed rule does not carry.
    expect(panel).toHaveTextContent(
      'policy rule "no-flat-vlan": guest NICs must carry a VLAN tag (op guest.nic.update: failed assertion params.vlan exists)',
    );
    expect(panel).not.toHaveTextContent(/asserts nothing/i);
  });

  it("says the assertions are unreadable when the installed set no longer carries the rule", async () => {
    fetchPolicies.mockResolvedValue({
      revision: 8,
      set: { version: 1, rules: [{ id: "something-else", description: "", severity: "warn", match: [] }] },
    });
    testPolicy.mockResolvedValue(denyResult);
    renderReview(baseChangeset());

    const panel = await screen.findByRole("region", { name: /policy verdict/i });
    await within(panel).findByTestId("policy-assert-unknown-no-flat-vlan");
    // The stronger claim — "this rule asserts nothing" — must not be made.
    expect(panel).not.toHaveTextContent(/matching it is itself the violation/i);
  });

  it("never renders an unreadable policy set as 'nothing applies'", async () => {
    fetchPolicies.mockRejectedValue(new Error("the daemon is unreachable"));
    renderReview(baseChangeset());

    const panel = await screen.findByRole("region", { name: /policy verdict/i });
    await waitFor(() => {
      expect(panel).toHaveTextContent(/could not be read/i);
    });
    expect(panel).toHaveTextContent("the daemon is unreachable");
    expect(panel).not.toHaveTextContent(/none of them objects/i);
  });
});

describe("ReviewApplyScreen — break-glass (T-3002 AC2)", () => {
  it("is not one click from the blocked state, and states the consequence before any confirm", async () => {
    const user = userEvent.setup();
    renderReview(baseChangeset({ approval: twoPersonBlocked() }));

    const panel = await screen.findByRole("region", { name: /emergency break-glass/i });

    // Click 0: nothing that records an override exists, and neither does the
    // reason field.
    expect(within(panel).queryByRole("button", { name: /record break-glass/i })).toBeNull();
    expect(within(panel).queryByLabelText(/break-glass reason/i)).toBeNull();
    expect(within(panel).queryByTestId("break-glass-consequences")).toBeNull();

    // Click 1 only reveals what it does. Still no confirm control.
    await user.click(within(panel).getByRole("button", { name: /read what break-glass does/i }));
    const consequences = within(panel).getByTestId("break-glass-consequences");
    expect(consequences).toHaveTextContent("change.breakglass");
    expect(consequences).toHaveTextContent(/24 hours/);
    expect(consequences).toHaveTextContent(/distinct-approver count and nothing else/i);
    expect(within(panel).queryByRole("button", { name: /record break-glass/i })).toBeNull();

    // Click 2 acknowledges it. Only now does the confirm control exist — and
    // the consequence text is still on screen beside it.
    await user.click(within(panel).getByRole("button", { name: /i understand/i }));
    expect(within(panel).getByTestId("break-glass-consequences")).toBeInTheDocument();
    const record = within(panel).getByRole("button", { name: /record break-glass/i });
    expect(record).toBeDisabled();
    expect(invokeBreakGlass).not.toHaveBeenCalled();
  });

  it("requires a typed reason and sends it", async () => {
    const user = userEvent.setup();
    renderReview(baseChangeset({ approval: twoPersonBlocked() }));

    const panel = await screen.findByRole("region", { name: /emergency break-glass/i });
    await user.click(within(panel).getByRole("button", { name: /read what break-glass does/i }));
    await user.click(within(panel).getByRole("button", { name: /i understand/i }));

    // Whitespace is not a reason.
    await user.type(within(panel).getByLabelText(/break-glass reason/i), "   ");
    expect(within(panel).getByRole("button", { name: /record break-glass/i })).toBeDisabled();

    await user.clear(within(panel).getByLabelText(/break-glass reason/i));
    await user.type(within(panel).getByLabelText(/break-glass reason/i), "core switch down, second approver on a plane");
    const record = within(panel).getByRole("button", { name: /record break-glass/i });
    expect(record).toBeEnabled();
    await user.click(record);

    await waitFor(() => {
      expect(invokeBreakGlass).toHaveBeenCalledWith("cs-gov", "core switch down, second approver on a plane");
    });
  });

  it("is not offered when the two-person rule is not what blocks", async () => {
    renderReview(baseChangeset({ approval: { status: "none", required: false } }));
    await screen.findByRole("region", { name: /policy verdict/i });
    expect(screen.queryByRole("region", { name: /emergency break-glass/i })).toBeNull();
  });

  it("shows an override already on record, including when its finding can be acknowledged", async () => {
    const approval = twoPersonBlocked();
    renderReview(
      baseChangeset({
        approval: {
          ...approval,
          twoPerson: {
            ...(approval.twoPerson ?? { required: 2, satisfied: false }),
            breakGlass: {
              changesetId: "cs-gov",
              reason: "core switch down",
              invokedBy: "brian@pam",
              invokedAt: 1_700_000_000,
              ackableAt: 1_700_086_400,
            },
          },
        },
      }),
    );

    const panel = await screen.findByRole("region", { name: /break-glass override on record/i });
    expect(panel).toHaveTextContent("brian@pam");
    expect(panel).toHaveTextContent("core switch down");
    // And it offers no way to take a second one.
    expect(screen.queryByRole("button", { name: /record break-glass/i })).toBeNull();
  });
});
