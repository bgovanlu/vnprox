// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Banner } from "./Banner";

describe("Banner", () => {
  it("renders its message and uses the tone's soft-wash token classes", () => {
    render(<Banner tone="degraded">No LLDP data yet.</Banner>);
    const banner = screen.getByRole("alert");
    expect(banner).toHaveTextContent("No LLDP data yet.");
    expect(banner.className).toContain("border-status-degraded");
    expect(banner.className).toContain("bg-status-degraded-soft");
    expect(banner.className).toContain("text-status-degraded");
  });

  it("defaults to role=alert for degraded/critical and role=status otherwise", () => {
    render(<Banner tone="critical">bad</Banner>);
    expect(screen.getByText("bad").closest("[role]")).toHaveAttribute("role", "alert");
  });

  it("info/ok/unknown default to role=status", () => {
    render(<Banner tone="ok">fine</Banner>);
    expect(screen.getByText("fine").closest("[role]")).toHaveAttribute("role", "status");
  });

  it("an explicit role overrides the tone default", () => {
    render(
      <Banner tone="ok" role="alert">
        fine, but urgent
      </Banner>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("renders a title separately from the body when given", () => {
    render(
      <Banner tone="info" title="Heads up">
        details here
      </Banner>,
    );
    expect(screen.getByText("Heads up")).toBeInTheDocument();
    expect(screen.getByText("details here")).toBeInTheDocument();
  });

  it("renders the leading badge pill when given", () => {
    render(
      <Banner tone="degraded" badge="Offline">
        Showing last-known data.
      </Banner>,
    );
    expect(screen.getByText("Offline")).toBeInTheDocument();
  });

  it("wires an accessible dismiss button when onDismiss is given", async () => {
    const onDismiss = vi.fn();
    const user = userEvent.setup();
    render(
      <Banner tone="ok" onDismiss={onDismiss}>
        applied
      </Banner>,
    );
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("renders no dismiss button when onDismiss is absent", () => {
    render(<Banner tone="ok">applied</Banner>);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("defaults to comfortable density, byte-for-byte the pre-T-4207 padding/gap/text size", () => {
    render(<Banner tone="ok">applied</Banner>);
    const banner = screen.getByRole("status");
    expect(banner.dataset.density).toBe("comfortable");
    expect(banner.className).toContain("gap-2 px-3 py-2 text-sm");
  });

  it("compact density renders tighter padding/gap and smaller text than comfortable", () => {
    render(
      <Banner tone="ok" density="compact">
        applied
      </Banner>,
    );
    const banner = screen.getByRole("status");
    expect(banner.dataset.density).toBe("compact");
    expect(banner.className).toContain("gap-1.5 px-2 py-1.5 text-xs");
    expect(banner.className).not.toContain("px-3 py-2");
  });
});
