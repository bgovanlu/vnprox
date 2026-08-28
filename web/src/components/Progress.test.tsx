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
});
