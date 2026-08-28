// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Progress } from "./Progress";

describe("Progress", () => {
  it("single-value mode renders role=progressbar with the clamped percentage", () => {
    render(<Progress value={42} label="Utilization" />);
    const bar = screen.getByRole("progressbar", { name: "Utilization" });
    expect(bar).toHaveAttribute("aria-valuenow", "42");
    expect(bar).toHaveAttribute("aria-valuemin", "0");
    expect(bar).toHaveAttribute("aria-valuemax", "100");
  });

  it("clamps out-of-range values to 0-100", () => {
    const { rerender } = render(<Progress value={150} label="x" />);
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "100");
    rerender(<Progress value={-10} label="x" />);
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
  });

  it("showValueText renders the rounded percentage as text", () => {
    render(<Progress value={33.6} label="x" showValueText />);
    expect(screen.getByText("34%")).toBeInTheDocument();
  });

  it("tone selects the fill's status token", () => {
    render(<Progress value={10} tone="critical" label="x" />);
    const fill = screen.getByRole("progressbar").firstElementChild;
    expect(fill?.className).toContain("bg-status-critical-solid");
  });

  it("segments mode renders one block per segment with the caller's own class, and no progressbar role", () => {
    const { container } = render(
      <Progress
        segments={[
          { percent: 60, className: "bg-accent-500", label: "allocated" },
          { percent: 20, className: "bg-slate-400", label: "reserved" },
        ]}
      />,
    );
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(container.querySelectorAll("[title]")).toHaveLength(2);
    expect(container.querySelector('[title="allocated"]')?.className).toContain("bg-accent-500");
  });

  it("defaults to comfortable density, byte-for-byte the pre-T-4207 track height and gap", () => {
    const { container } = render(<Progress value={42} label="x" />);
    const bar = screen.getByRole("progressbar");
    const root = container.querySelector("[data-density]");
    expect(root?.getAttribute("data-density")).toBe("comfortable");
    expect(root?.className).toContain("gap-2");
    expect(bar.className).toContain("h-1.5");
  });

  it("compact density renders a shorter track and tighter gap than comfortable", () => {
    const { container } = render(<Progress value={42} label="x" density="compact" />);
    const bar = screen.getByRole("progressbar");
    const root = container.querySelector("[data-density]");
    expect(root?.getAttribute("data-density")).toBe("compact");
    expect(root?.className).toContain("gap-1");
    expect(root?.className).not.toContain("gap-2");
    expect(bar.className).toContain("h-1");
    expect(bar.className).not.toContain("h-1.5");
  });
});
