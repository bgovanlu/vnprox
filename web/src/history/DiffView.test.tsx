import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { DiffView } from "./DiffView";
import { parseUnifiedDiff } from "./parseDiff";

const sample = [
  "--- /etc/network/interfaces",
  "+++ /etc/network/interfaces",
  "@@ -1,3 +1,4 @@",
  " auto lo",
  " iface lo inet loopback",
  "-iface vmbr9 inet manual",
  "+iface vmbr1 inet manual",
  "+\tbridge-ports eno1",
  "",
].join("\n");

describe("parseUnifiedDiff", () => {
  it("classifies headers, hunks, adds, removes, and context", () => {
    const lines = parseUnifiedDiff(sample);
    expect(lines.map((l) => l.kind)).toEqual([
      "header",
      "header",
      "hunk",
      "context",
      "context",
      "remove",
      "add",
      "add",
    ]);
  });

  it("returns nothing for an empty diff", () => {
    expect(parseUnifiedDiff("")).toEqual([]);
  });

  it("does not misclassify +++/--- headers as add/remove lines", () => {
    const lines = parseUnifiedDiff("--- a\n+++ b\n");
    expect(lines.every((l) => l.kind === "header")).toBe(true);
  });
});

describe("DiffView", () => {
  it("renders every diff line", () => {
    render(<DiffView unified={sample} />);
    expect(screen.getByText("+iface vmbr1 inet manual")).toBeInTheDocument();
    expect(screen.getByText("-iface vmbr9 inet manual")).toBeInTheDocument();
    expect(screen.getByText("@@ -1,3 +1,4 @@")).toBeInTheDocument();
  });

  it("shows a no-differences message for an empty diff", () => {
    render(<DiffView unified="" />);
    expect(screen.getByText("No differences.")).toBeInTheDocument();
  });
});
