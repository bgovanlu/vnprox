// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Stat } from "./Stat";

describe("Stat", () => {
  it("renders the value, label and description", () => {
    render(<Stat label="Drift findings" value={3} description="across 2 nodes" />);
    expect(screen.getByText("Drift findings")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("across 2 nodes")).toBeInTheDocument();
  });

  it("renders a status dot only when a status is given", () => {
    const { container, rerender } = render(<Stat value={1} label="x" />);
    expect(container.querySelector("[aria-hidden]")).not.toBeInTheDocument();

    rerender(<Stat value={1} label="x" status="critical" />);
    const dot = container.querySelector("[aria-hidden]");
    expect(dot).not.toBeNull();
    expect(dot?.className).toContain("bg-status-critical");
  });

  it("renders a non-numeric value verbatim, with no reformatting", () => {
    render(<Stat value="87%" label="Utilization" />);
    expect(screen.getByText("87%")).toBeInTheDocument();
  });
});
