// T-2402 / T-2408 frontend tests.
//
// The central claim these pin is the invariant: ACKNOWLEDGEMENT IS NOT
// SUPPRESSION. A UI that quietly filtered acknowledged findings out of the
// default view would satisfy every "the badge renders" assertion while
// defeating the whole design, so the first test here asserts the finding is
// still present, not merely that a badge appeared.
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AckDialog } from "./AckDialog";
import { expiryFromDays } from "./ackExpiry";
import { FindingsList, type FindingItem } from "./FindingsList";

const acked: FindingItem = {
  id: "health:mtu_mismatch|bridge:pve1:vmbr0",
  severity: "warning",
  detail: "bridge vmbr0 MTU differs from its peers",
  nodes: ["pve1"],
  fixable: true,
  category: "mtu_mismatch",
  ack: { reason: "jumbo on storage only", ackedBy: "brian", ackedAt: 1_700_000_000, expiresAt: 1_700_086_400 },
};

const open: FindingItem = {
  id: "health:mtu_mismatch|bridge:pve2:vmbr0",
  severity: "warning",
  detail: "bridge vmbr0 on pve2 MTU differs from its peers",
  nodes: ["pve2"],
  fixable: true,
  category: "mtu_mismatch",
};

describe("FindingsList — acknowledgement (T-2402)", () => {
  it("keeps an acknowledged finding in the list, marked and explained", () => {
    render(<FindingsList findings={[acked, open]} />);

    // Not suppression: BOTH findings are still rendered.
    expect(screen.getByText(acked.detail)).toBeInTheDocument();
    expect(screen.getByText(open.detail)).toBeInTheDocument();

    expect(screen.getByText("Acknowledged")).toBeInTheDocument();
    // The reason is visible, not just the fact — an unexplained mute is the
    // thing T-2402 exists to avoid.
    expect(screen.getByText(/jumbo on storage only/)).toBeInTheDocument();
    expect(screen.getByText(/brian/)).toBeInTheDocument();
  });

  it("offers Acknowledge on an open finding and Un-acknowledge on an acked one, never both", async () => {
    const user = userEvent.setup();
    const onAck = vi.fn();
    const onUnack = vi.fn();
    render(<FindingsList findings={[acked, open]} onAck={onAck} onUnack={onUnack} />);

    const items = screen.getAllByRole("listitem");
    const ackedRow = within(items[0]);
    const openRow = within(items[1]);

    expect(ackedRow.queryByRole("button", { name: "Acknowledge" })).not.toBeInTheDocument();
    expect(openRow.queryByRole("button", { name: "Un-acknowledge" })).not.toBeInTheDocument();

    await user.click(openRow.getByRole("button", { name: "Acknowledge" }));
    expect(onAck).toHaveBeenCalledWith(open.id);

    await user.click(ackedRow.getByRole("button", { name: "Un-acknowledge" }));
    expect(onUnack).toHaveBeenCalledWith(acked.id);
  });

  it("hides the fix button on an acknowledged finding", () => {
    render(<FindingsList findings={[acked, open]} onFix={vi.fn()} />);
    const items = screen.getAllByRole("listitem");
    expect(within(items[0]).queryByRole("button", { name: /fixing changeset/i })).not.toBeInTheDocument();
    // Control: the un-acknowledged one still offers it, so this is not just a
    // test of a button that never renders.
    expect(within(items[1]).getByRole("button", { name: /fixing changeset/i })).toBeInTheDocument();
  });
});

describe("FindingsList — batch selection (T-2408)", () => {
  it("offers a checkbox only for fixable, un-acknowledged findings", () => {
    const unfixable: FindingItem = { ...open, id: "x", fixable: false, detail: "not fixable" };
    render(
      <FindingsList
        findings={[acked, open, unfixable]}
        selectedIds={new Set()}
        onToggleSelected={vi.fn()}
      />,
    );
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(1);
    expect(boxes[0]).toHaveAccessibleName(`Select finding: ${open.detail}`);
  });

  it("reflects and toggles the caller's selection", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <FindingsList
        findings={[open]}
        selectedIds={new Set([open.id])}
        onToggleSelected={onToggle}
      />,
    );
    const box = screen.getByRole("checkbox");
    expect(box).toBeChecked();
    await user.click(box);
    expect(onToggle).toHaveBeenCalledWith(open.id);
  });

  it("renders no checkboxes at all when the caller offers no batch action", () => {
    render(<FindingsList findings={[open]} onFix={vi.fn()} />);
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});

describe("AckDialog", () => {
  it("will not confirm without a reason", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<AckDialog finding={{ id: "f1", detail: "d" }} onCancel={vi.fn()} onConfirm={onConfirm} />);

    const confirm = screen.getByRole("button", { name: "Acknowledge" });
    expect(confirm).toBeDisabled();

    // Whitespace is not a reason.
    await user.type(screen.getByRole("textbox"), "   ");
    expect(confirm).toBeDisabled();

    await user.type(screen.getByRole("textbox"), "deliberate");
    expect(confirm).toBeEnabled();
    await user.click(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm.mock.calls[0][0]).toBe("deliberate");
  });

  it("passes a future expiry for a bounded choice and undefined for 'No expiry'", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<AckDialog finding={{ id: "f1", detail: "d" }} onCancel={vi.fn()} onConfirm={onConfirm} />);

    await user.type(screen.getByRole("textbox"), "because");
    await user.click(screen.getByRole("button", { name: "Acknowledge" }));
    const [, expiresAt] = onConfirm.mock.calls[0] as [string, number | undefined];
    expect(expiresAt).toBeDefined();
    // The point of the default: it is in the future, so the server's
    // already-in-the-past refusal is unreachable from this dialog.
    expect(expiresAt).toBeGreaterThan(Math.floor(Date.now() / 1000));

    await user.selectOptions(screen.getByLabelText("Acknowledgement expiry"), "0");
    await user.type(screen.getByRole("textbox"), "because");
    await user.click(screen.getByRole("button", { name: "Acknowledge" }));
    const [, noExpiry] = onConfirm.mock.calls[1] as [string, number | undefined];
    expect(noExpiry).toBeUndefined();
  });

  it("is closed when no finding is being acknowledged", () => {
    render(<AckDialog onCancel={vi.fn()} onConfirm={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Acknowledge" })).not.toBeInTheDocument();
  });
});

describe("expiryFromDays", () => {
  it("converts days to unix seconds from the given instant", () => {
    const now = new Date("2026-08-08T00:00:00Z");
    expect(expiryFromDays(30, now)).toBe(Math.floor(now.getTime() / 1000) + 30 * 86_400);
    expect(expiryFromDays(1, now)).toBe(Math.floor(now.getTime() / 1000) + 86_400);
  });

  it("treats zero and negatives as 'no expiry' rather than an instant in the past", () => {
    const now = new Date("2026-08-08T00:00:00Z");
    expect(expiryFromDays(0, now)).toBeUndefined();
    expect(expiryFromDays(-5, now)).toBeUndefined();
  });
});
