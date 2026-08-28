// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { KeyValue } from "./KeyValue";

describe("KeyValue", () => {
  it("renders one dt/dd pair per item", () => {
    render(
      <KeyValue
        items={[
          { key: "source", label: "Source", value: "10.0.0.1" },
          { key: "dest", label: "Destination", value: "10.0.0.2" },
        ]}
      />,
    );
    expect(screen.getByText("Source")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument();
    expect(screen.getByText("Destination")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.2")).toBeInTheDocument();
  });

  it("defaults to a single auto/1fr column pair", () => {
    const { container } = render(<KeyValue items={[{ key: "a", label: "A", value: "1" }]} />);
    expect(container.querySelector("dl")?.className).toContain("grid-cols-[auto_1fr]");
  });

  it("columns=2 doubles the auto/1fr pair", () => {
    const { container } = render(<KeyValue items={[{ key: "a", label: "A", value: "1" }]} columns={2} />);
    expect(container.querySelector("dl")?.className).toContain("grid-cols-[auto_1fr_auto_1fr]");
  });

  it("mono items get font-mono tabular-nums on the value", () => {
    render(<KeyValue items={[{ key: "mac", label: "MAC", value: "aa:bb:cc:dd:ee:ff", mono: true }]} />);
    const dd = screen.getByText("aa:bb:cc:dd:ee:ff");
    expect(dd.className).toContain("font-mono");
    expect(dd.className).toContain("tabular-nums");
  });

  it("each item is wrapped so its dt/dd participate directly in the outer grid", () => {
    const { container } = render(
      <KeyValue
        items={[
          { key: "a", label: "A", value: "1" },
          { key: "b", label: "B", value: "2" },
        ]}
      />,
    );
    const wrappers = container.querySelectorAll("dl > .contents");
    expect(wrappers).toHaveLength(2);
  });
});
