import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ContextMenu } from "./ContextMenu";

describe("ContextMenu", () => {
  it("renders nothing for an empty item list", () => {
    const { container } = render(<ContextMenu x={0} y={0} items={[]} onClose={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders every item and invokes onSelect + onClose when clicked", async () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(<ContextMenu x={10} y={20} items={[{ label: "Trace path from here", onSelect }]} onClose={onClose} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("menuitem", { name: "Trace path from here" }));
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("closes on an outside click", async () => {
    const onClose = vi.fn();
    render(
      <div>
        <button type="button">outside</button>
        <ContextMenu x={0} y={0} items={[{ label: "Item", onSelect: vi.fn() }]} onClose={onClose} />
      </div>,
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "outside" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on Escape", async () => {
    const onClose = vi.fn();
    render(<ContextMenu x={0} y={0} items={[{ label: "Item", onSelect: vi.fn() }]} onClose={onClose} />);
    const user = userEvent.setup();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});
