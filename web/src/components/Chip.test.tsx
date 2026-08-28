// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Chip } from "./Chip";

describe("Chip", () => {
  it("renders its children, mono by default", () => {
    render(<Chip>automation</Chip>);
    const chip = screen.getByText("automation");
    expect(chip.className).toContain("font-mono");
  });

  it("opts out of mono for a plain-text tag", () => {
    render(<Chip mono={false}>free text</Chip>);
    expect(screen.getByText("free text").className).not.toContain("font-mono");
  });

  it("removed tone strikes the name through", () => {
    render(<Chip tone="removed">audit</Chip>);
    expect(screen.getByText("audit").className).toContain("line-through");
  });

  it("accent tone is distinct from neutral", () => {
    render(<Chip tone="accent">free</Chip>);
    expect(screen.getByText("free").className).toContain("border-accent-500");
  });

  it("renders no remove button when onRemove is absent", () => {
    render(<Chip>scope</Chip>);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("renders and wires an accessible remove button when onRemove is given", async () => {
    const onRemove = vi.fn();
    const user = userEvent.setup();
    render(
      <Chip onRemove={onRemove} removeLabel="Remove automation">
        automation
      </Chip>,
    );
    await user.click(screen.getByRole("button", { name: "Remove automation" }));
    expect(onRemove).toHaveBeenCalledTimes(1);
  });
});
