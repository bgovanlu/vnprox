// Phase 36 Tier 2's ceremony. An operational action has no changeset to
// review, so the confirmation dialog is the only place an operator is told
// what is about to happen — which makes "does it actually confirm" a
// correctness property, not a UX nicety.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NodeResultsList, OperationalActionButton } from "./OperationalActionButton";

function renderButton(over: Partial<Parameters<typeof OperationalActionButton>[0]> = {}) {
  const onConfirm = vi.fn();
  render(
    <OperationalActionButton
      label="Install lldpd on all nodes"
      title="Install lldpd on every node?"
      description="vnprox will install and enable lldpd on every reachable node."
      confirmLabel="Install"
      onConfirm={onConfirm}
      {...over}
    />,
  );
  return { onConfirm };
}

describe("OperationalActionButton", () => {
  it("does not fire the action until the operator confirms", async () => {
    // The property this whole component exists for: clicking the banner's
    // button must open a dialog, not mutate a cluster.
    const user = userEvent.setup();
    const { onConfirm } = renderButton();
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    expect(onConfirm).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("names the blast radius in the dialog", async () => {
    // "installs on all nodes" and "installs on pve1" are different
    // sentences, and an operator who cannot tell them apart from the dialog
    // has not really been asked.
    const user = userEvent.setup();
    renderButton();
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    expect(screen.getByText(/every reachable node/)).toBeInTheDocument();
  });

  it("cancels without firing", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderButton();
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("disables both buttons while the mutation is in flight", async () => {
    const user = userEvent.setup();
    renderButton({ pending: true });
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    // A second click while the first is still running would fan out twice.
    expect(screen.getByRole("button", { name: "Working…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });

  it("keeps the dialog open while pending, so the result has somewhere to land", async () => {
    const user = userEvent.setup();
    renderButton({ pending: true });
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "Working…" })).toBeInTheDocument();
  });
});

describe("NodeResultsList", () => {
  it("reports a partial failure as a failure, not as done", () => {
    // The honesty requirement in T-3602's card: two of five failing is not
    // success, and the summary line must not round it to one.
    render(
      <NodeResultsList
        results={[
          { node: "pve1", ok: true },
          { node: "pve2", ok: false, error: "E: Unable to locate package lldpd" },
          { node: "pve3", ok: true },
        ]}
      />,
    );
    expect(screen.getByText("Failed on 1 of 3 nodes.")).toBeInTheDocument();
    expect(screen.getByText(/Unable to locate package lldpd/)).toBeInTheDocument();
  });

  it("lists the nodes that succeeded too", () => {
    // "3 succeeded" with no list is indistinguishable from "3 of 5 were
    // even attempted".
    render(<NodeResultsList results={[{ node: "pve1", ok: true }, { node: "pve2", ok: true }]} />);
    expect(screen.getByText("Succeeded on all 2 nodes.")).toBeInTheDocument();
    expect(screen.getByText("pve1")).toBeInTheDocument();
    expect(screen.getByText("pve2")).toBeInTheDocument();
  });

  it("says the state in words, not only in the dot's colour", () => {
    // WCAG 1.4.1 — the same rule the faceplate's port LEDs follow.
    render(<NodeResultsList results={[{ node: "pve1", ok: false, error: "boom" }]} />);
    expect(screen.getByText("failed")).toBeInTheDocument();
  });
});
