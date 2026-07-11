import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ResolvedViewTable } from "./ResolvedViewTable";
import type { ResolvedView } from "../api/types";

/** noUncheckedIndexedAccess makes every array index read `T | undefined`;
 * this test file always indexes into a slice it has just asserted the
 * length of, so a thrown assertion (not a silent `undefined`) is the
 * right failure mode for an out-of-range read here. */
function at<T>(arr: T[], i: number): T {
  const v = arr[i];
  if (v === undefined) {
    throw new Error(`expected an element at index ${String(i)}, got undefined`);
  }
  return v;
}

const baseView: ResolvedView = {
  guest: "guest:pve1:100",
  active: true,
  rules: [
    { pos: 0, origin: "cluster", rule: { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "22" } },
    {
      pos: 1, origin: "cluster",
      rule: { pos: 1, enabled: true, direction: "group", action: "webservers" },
      groupName: "webservers",
    },
    {
      pos: 2, origin: "group", groupName: "webservers",
      rule: { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "80" },
    },
    { pos: 3, origin: "guest", rule: { pos: 0, enabled: true, direction: "in", action: "DROP", proto: "tcp", dport: "80" } },
  ],
  defaultIn: { direction: "in", policy: "DROP", origin: "cluster" },
  defaultOut: { direction: "out", policy: "ACCEPT", origin: "cluster" },
};

describe("ResolvedViewTable", () => {
  it("renders every resolved rule in the server-provided order with origin labels", () => {
    render(<ResolvedViewTable resolved={baseView} />);
    const rows = screen.getAllByRole("row").slice(1); // drop the header row
    // 4 rule rows + 2 default-policy rows.
    expect(rows).toHaveLength(6);

    expect(within(at(rows, 0)).getByText("Cluster")).toBeInTheDocument();
    expect(within(at(rows, 1)).getByText("Cluster: webservers")).toBeInTheDocument();
    expect(within(at(rows, 2)).getByText("Security group: webservers")).toBeInTheDocument();
    expect(within(at(rows, 3)).getByText("Guest")).toBeInTheDocument();
  });

  it("always renders the default policies last, labeled with their origin", () => {
    render(<ResolvedViewTable resolved={baseView} />);
    const rows = screen.getAllByRole("row").slice(1);
    const lastTwo = rows.slice(-2);
    expect(within(at(lastTwo, 0)).getByText("DROP")).toBeInTheDocument();
    expect(within(at(lastTwo, 0)).getByText(/from cluster/)).toBeInTheDocument();
    expect(within(at(lastTwo, 1)).getByText("ACCEPT")).toBeInTheDocument();
  });

  it("dims a disabled rule visually but still shows it (transparency, not filtering)", () => {
    const view: ResolvedView = {
      ...baseView,
      rules: [
        { pos: 0, origin: "guest", rule: { pos: 0, enabled: false, direction: "in", action: "ACCEPT", proto: "tcp", dport: "22" } },
      ],
    };
    render(<ResolvedViewTable resolved={view} />);
    const row = at(screen.getAllByRole("row"), 1);
    expect(row).toHaveClass("opacity-50");
    expect(within(row).getByText("ACCEPT")).toBeInTheDocument();
  });

  it("renders an empty state plus the fallthrough defaults when no rules apply", () => {
    const view: ResolvedView = { ...baseView, rules: [] };
    render(<ResolvedViewTable resolved={view} />);
    expect(screen.getByText("No rules apply")).toBeInTheDocument();
    expect(screen.getByText("DROP")).toBeInTheDocument();
    expect(screen.getByText("ACCEPT")).toBeInTheDocument();
  });
});
