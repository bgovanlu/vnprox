import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Button } from "./Button";

describe("Button", () => {
  it("renders its children and forwards a click handler", async () => {
    const onClick = vi.fn();
    const user = userEvent.setup();
    render(<Button onClick={onClick}>Apply changes</Button>);

    const button = screen.getByRole("button", { name: "Apply changes" });
    await user.click(button);

    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("defaults to type=button so it never submits a form by accident", () => {
    render(<Button>Cancel</Button>);
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveAttribute("type", "button");
  });

  it("disables interaction when disabled", async () => {
    const onClick = vi.fn();
    const user = userEvent.setup();
    render(
      <Button disabled onClick={onClick}>
        Apply
      </Button>,
    );

    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(onClick).not.toHaveBeenCalled();
  });

  describe("T-3405 shape", () => {
    it("defaults to the pre-T-3405 rounded-md shape, unchanged", () => {
      render(<Button>Apply</Button>);
      const button = screen.getByRole("button", { name: "Apply" });
      expect(button.className).toContain("rounded-md");
      expect(button.className).not.toContain("rounded-pill");
    });

    it("shape=\"pill\" applies the pill radius instead of rounded-md", () => {
      render(<Button shape="pill">Add funds</Button>);
      const button = screen.getByRole("button", { name: "Add funds" });
      expect(button.className).toContain("rounded-pill");
      expect(button.className).not.toContain("rounded-md");
    });
  });

  describe("T-905 density", () => {
    it("defaults to comfortable (unchanged from this component's original spacing)", () => {
      render(<Button>Apply</Button>);
      const button = screen.getByRole("button", { name: "Apply" });
      expect(button).toHaveAttribute("data-density", "comfortable");
      expect(button.className).toContain("h-9");
    });

    it("compact tightens height/padding distinctly from comfortable, at the same size", () => {
      render(<Button density="compact">Apply</Button>);
      const button = screen.getByRole("button", { name: "Apply" });
      expect(button).toHaveAttribute("data-density", "compact");
      expect(button.className).toContain("h-8");
      expect(button.className).not.toContain("h-9");
    });
  });
});
