import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { RuleView } from "../api/types";
import { RuleTable } from "./RuleTable";

const rules: RuleView[] = [
  { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "22" },
  { pos: 1, enabled: true, direction: "in", action: "DROP", proto: "tcp", dport: "80", comment: "override" },
];

describe("RuleTable", () => {
  it("renders an empty state for no rules", () => {
    render(<RuleTable rules={[]} />);
    expect(screen.getByText("No rules")).toBeInTheDocument();
  });

  it("renders every rule", () => {
    render(<RuleTable rules={rules} />);
    expect(screen.getByText("override")).toBeInTheDocument();
  });

  describe("focusPos (T-504/T-505 deep link)", () => {
    it("marks the row at focusPos and no other", () => {
      render(<RuleTable rules={rules} focusPos={1} />);
      const rows = screen.getAllByRole("row").slice(1);
      expect(rows[1]).toHaveAttribute("data-focused", "true");
      expect(rows[0]).not.toHaveAttribute("data-focused");
    });

    it("marks no row when focusPos is undefined", () => {
      render(<RuleTable rules={rules} />);
      for (const row of screen.getAllByRole("row")) {
        expect(row).not.toHaveAttribute("data-focused");
      }
    });
  });
});
