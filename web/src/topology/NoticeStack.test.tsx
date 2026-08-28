// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { NoticeStack, type Notice } from "./NoticeStack";

function notice(id: string, active: boolean, tone: Notice["tone"], label: string): Notice {
  return { id, active, tone, label, body: <div data-testid={`body-${id}`}>{label} body</div> };
}

describe("NoticeStack (T-4304)", () => {
  it("renders nothing when no notice applies", () => {
    const { container } = render(
      <NoticeStack
        label="Topology notices"
        notices={[notice("a", false, "info", "A"), notice("b", false, "degraded", "B")]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a single notice as itself, with no disclosure", () => {
    // The single-notice case is the common one and must not regress: a lone
    // banner is not a stack, and putting it behind a "Show" control would
    // make the ordinary page worse in order to fix the crowded one.
    render(
      <NoticeStack
        label="Topology notices"
        notices={[notice("a", true, "degraded", "A"), notice("b", false, "info", "B")]}
      />,
    );
    expect(screen.getByTestId("body-a")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("collapses two or more into one summary row, bodies hidden until asked for", () => {
    render(
      <NoticeStack
        label="Topology notices"
        notices={[
          notice("a", true, "degraded", "Last-known data"),
          notice("b", true, "critical", "3 off-map findings"),
          notice("c", true, "info", "No LLDP data"),
        ]}
      />,
    );
    const toggle = screen.getByRole("button", { expanded: false });
    expect(toggle).toHaveTextContent("3 notices");
    // Every notice is still NAMED in the collapsed row — collapsing must not
    // hide that a condition exists, only its detail.
    expect(toggle).toHaveTextContent("Last-known data");
    expect(toggle).toHaveTextContent("3 off-map findings");
    expect(toggle).toHaveTextContent("No LLDP data");
    expect(screen.queryByTestId("body-a")).not.toBeInTheDocument();

    fireEvent.click(toggle);
    expect(screen.getByRole("button", { expanded: true })).toBeInTheDocument();
    expect(screen.getByTestId("body-a")).toBeInTheDocument();
    expect(screen.getByTestId("body-b")).toBeInTheDocument();
    expect(screen.getByTestId("body-c")).toBeInTheDocument();
  });

  it("takes the summary row's tone from the most severe notice present", () => {
    // A collapsed row must not make a critical condition look routine. The
    // row is the only thing on screen until someone expands it, so it answers
    // for the worst thing inside it.
    const { container, rerender } = render(
      <NoticeStack
        label="Topology notices"
        notices={[notice("a", true, "info", "A"), notice("b", true, "critical", "B")]}
      />,
    );
    expect(container.querySelector("button")?.className).toContain("border-status-critical");

    rerender(
      <NoticeStack
        label="Topology notices"
        notices={[notice("a", true, "info", "A"), notice("b", true, "degraded", "B")]}
      />,
    );
    expect(container.querySelector("button")?.className).toContain("border-status-degraded");
  });

  it("never builds a tone class by interpolation", () => {
    // Tailwind v4 resolves utilities by scanning source text, so
    // `border-status-${tone}` is never emitted into the stylesheet: the row
    // would render with no border and no wash while this file's className
    // assertions all still passed. Asserting the source itself is the only
    // way to catch that from a jsdom test, which cannot resolve CSS at all.
    // Comments are stripped first. The doc comment in NoticeStack.tsx quotes
    // `border-status-${tone}` as the thing NOT to do, and a guard that fires
    // on prose describing the defect instead of the defect is worse than no
    // guard — it fails on the file that documents the rule best.
    const source = readFileSync(resolve(__dirname, "NoticeStack.tsx"), "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/^\s*\/\/.*$/gm, "");
    expect(source).not.toMatch(/`[^`]*-status-\$\{/);
    expect(source).not.toMatch(/`[^`]*-surface-\$\{/);
  });

  it("does not render inactive notices even when others are shown", () => {
    render(
      <NoticeStack
        label="Topology notices"
        notices={[
          notice("a", true, "degraded", "A"),
          notice("b", true, "info", "B"),
          notice("c", false, "critical", "C"),
        ]}
      />,
    );
    const toggle = screen.getByRole("button", { expanded: false });
    expect(toggle).toHaveTextContent("2 notices");
    expect(toggle).not.toHaveTextContent("C");
    fireEvent.click(toggle);
    expect(screen.queryByTestId("body-c")).not.toBeInTheDocument();
  });
});
