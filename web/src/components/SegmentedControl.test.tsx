// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SegmentedControl } from "./SegmentedControl";

const OPTIONS = [
  { value: "switch", label: "Switch" },
  { value: "graph", label: "Graph" },
  { value: "map", label: "Map" },
] as const;

describe("SegmentedControl", () => {
  it("renders a radiogroup with one radio per option, the active one checked", () => {
    render(<SegmentedControl options={OPTIONS} value="graph" onChange={vi.fn()} ariaLabel="View mode" />);
    const group = screen.getByRole("radiogroup", { name: "View mode" });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Switch" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByRole("radio", { name: "Graph" })).toHaveAttribute("aria-checked", "true");
  });

  it("calls onChange with the clicked option's value", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedControl options={OPTIONS} value="switch" onChange={onChange} ariaLabel="View mode" />);

    await user.click(screen.getByRole("radio", { name: "Map" }));
    expect(onChange).toHaveBeenCalledWith("map");
  });

  it("only the checked option is in the tab order (roving tabindex)", () => {
    render(<SegmentedControl options={OPTIONS} value="graph" onChange={vi.fn()} ariaLabel="View mode" />);
    expect(screen.getByRole("radio", { name: "Switch" })).toHaveAttribute("tabindex", "-1");
    expect(screen.getByRole("radio", { name: "Graph" })).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("radio", { name: "Map" })).toHaveAttribute("tabindex", "-1");
  });

  it("ArrowRight moves selection to the next option, wrapping at the end", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedControl options={OPTIONS} value="map" onChange={onChange} ariaLabel="View mode" />);

    screen.getByRole("radio", { name: "Map" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenCalledWith("switch");
  });

  it("ArrowLeft moves selection to the previous option, wrapping at the start", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedControl options={OPTIONS} value="switch" onChange={onChange} ariaLabel="View mode" />);

    screen.getByRole("radio", { name: "Switch" }).focus();
    await user.keyboard("{ArrowLeft}");
    expect(onChange).toHaveBeenCalledWith("map");
  });

  it("Home and End jump to the first/last option", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<SegmentedControl options={OPTIONS} value="graph" onChange={onChange} ariaLabel="View mode" />);

    screen.getByRole("radio", { name: "Graph" }).focus();
    await user.keyboard("{End}");
    expect(onChange).toHaveBeenLastCalledWith("map");
    await user.keyboard("{Home}");
    expect(onChange).toHaveBeenLastCalledWith("switch");
  });
});
