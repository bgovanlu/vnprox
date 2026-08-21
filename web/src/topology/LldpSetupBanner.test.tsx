// T-3602. The security-relevant assertion here is the capability gate: a
// read-only session must not be offered a button that installs software on
// every node in the cluster. That is the kind of thing that is obvious when
// you look and invisible when you don't, so it gets a test rather than a
// glance.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LldpSetupBanner } from "./LldpSetupBanner";

function renderBanner(over: Partial<Parameters<typeof LldpSetupBanner>[0]> = {}) {
  const onInstall = vi.fn();
  render(
    <LldpSetupBanner show canInstall pending={false} results={undefined} onInstall={onInstall} {...over} />,
  );
  return { onInstall };
}

describe("LldpSetupBanner", () => {
  it("renders nothing when LLDP data is already present", () => {
    const { container } = render(
      <LldpSetupBanner show={false} canInstall pending={false} results={undefined} onInstall={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("offers the install action to a session with netWrite", () => {
    renderBanner();
    expect(screen.getByRole("button", { name: "Install lldpd on all nodes" })).toBeInTheDocument();
  });

  it("offers NO button at all without netWrite, but keeps the documentation link", () => {
    // Not a disabled button. A read-only operator cannot act on a greyed-out
    // control and is not told why it is grey; the man-page link is the thing
    // they can actually use, and it is still right there.
    renderBanner({ canInstall: false });
    expect(screen.queryByRole("button", { name: "Install lldpd on all nodes" })).toBeNull();
    expect(screen.getByRole("link", { name: "Set up lldpd" })).toBeInTheDocument();
  });

  it("does not install until the operator confirms", async () => {
    const user = userEvent.setup();
    const { onInstall } = renderBanner();
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    expect(onInstall).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(onInstall).toHaveBeenCalledTimes(1);
  });

  it("says in the dialog that no changeset is staged", async () => {
    // Phase 36's Tier 2 contract made visible: this action is outside the
    // change engine because there is no PVE configuration to diff, and an
    // operator used to "stage → review → apply" deserves to be told that
    // this one does not work that way.
    const user = userEvent.setup();
    renderBanner();
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    expect(screen.getByText(/no changeset is staged/)).toBeInTheDocument();
    expect(screen.getByText(/audit log/)).toBeInTheDocument();
  });

  it("shows per-node results after a partial failure", async () => {
    const user = userEvent.setup();
    renderBanner({
      results: [
        { node: "pve1", ok: true },
        { node: "pve2", ok: false, error: "E: Unable to locate package lldpd" },
      ],
    });
    await user.click(screen.getByRole("button", { name: "Install lldpd on all nodes" }));
    expect(screen.getByText("Failed on 1 of 2 nodes.")).toBeInTheDocument();
    expect(screen.getByText(/Unable to locate package lldpd/)).toBeInTheDocument();
  });
});
