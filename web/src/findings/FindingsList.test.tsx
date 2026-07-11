import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { FindingsList } from "./FindingsList";
import type { FindingItem } from "./FindingsList";

const fixable: FindingItem = {
  id: "mtu_consistency|bridge:pve2:vmbr0",
  severity: "warning",
  detail: "bridge vmbr0 MTU has drifted across the cluster",
  nodes: ["pve1", "pve2", "pve3"],
  refs: ["bridge:pve2:vmbr0"],
  fixable: true,
  category: "mtu_consistency",
};

const unfixable: FindingItem = {
  id: "sdn_realization|zone-legacy",
  severity: "error",
  detail: "SDN zone zone-legacy lists pve3 as member but bridge vmbr99 is not realized there",
  nodes: ["pve3"],
  fixable: false,
  category: "sdn_realization",
};

describe("FindingsList", () => {
  it("shows an empty state when there are no findings", () => {
    render(<FindingsList findings={[]} />);
    expect(screen.getByText("No findings")).toBeInTheDocument();
  });

  it("renders every finding's severity, detail, nodes, and category", () => {
    render(<FindingsList findings={[fixable, unfixable]} />);
    expect(screen.getByText(fixable.detail)).toBeInTheDocument();
    expect(screen.getByText(unfixable.detail)).toBeInTheDocument();
    expect(screen.getByText("Warning")).toBeInTheDocument();
    expect(screen.getByText("Error")).toBeInTheDocument();
    expect(screen.getByText("pve1, pve2, pve3")).toBeInTheDocument();
  });

  it("renders a fix button only for fixable findings, and calls onFix with its id", async () => {
    const user = userEvent.setup();
    const onFix = vi.fn();
    render(<FindingsList findings={[fixable, unfixable]} onFix={onFix} />);

    const button = screen.getByRole("button", { name: "Create fixing changeset" });

    await user.click(button);
    expect(onFix).toHaveBeenCalledWith(fixable.id);
  });

  it("does not render any fix button when onFix is omitted", () => {
    render(<FindingsList findings={[fixable]} />);
    expect(screen.queryByRole("button", { name: "Create fixing changeset" })).not.toBeInTheDocument();
  });

  it("disables and relabels the button for the finding currently being fixed", () => {
    render(<FindingsList findings={[fixable]} onFix={vi.fn()} fixingId={fixable.id} />);
    const button = screen.getByRole("button", { name: "Creating…" });
    expect(button).toBeDisabled();
  });
});
