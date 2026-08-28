// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Badge } from "./Badge";

describe("Badge", () => {
  it("defaults to the soft role", () => {
    render(<Badge status="critical">2 findings</Badge>);
    const badge = screen.getByText("2 findings");
    expect(badge.className).toContain("bg-status-critical-soft");
    expect(badge.className).toContain("text-status-critical");
    expect(badge.className).not.toContain("-solid");
  });

  it("solid role takes its text colour from a token, with no dark: variant at all", () => {
    // This started as `text-white dark:text-slate-900`, which was CORRECT
    // — white on the dark-mode solid steps measures 2.36-3.66:1, all below
    // AA, so the two themes genuinely need opposite text colours. But
    // spelling that out at a call site is the conditional this whole token
    // system exists to delete, and it only works for as long as everyone
    // remembers the inversion. `--color-status-on-solid` is defined per
    // theme instead, and index.css.test.ts asserts it clears AA against
    // every solid fill in both. So the assertion here is the absence of a
    // `dark:` override, not the presence of the right one.
    render(
      <Badge status="ok" role="solid">
        Up
      </Badge>,
    );
    const badge = screen.getByText("Up");
    expect(badge.className).toContain("bg-status-ok-solid");
    expect(badge.className).toContain("text-status-on-solid");
    expect(badge.className).not.toContain("dark:");
  });

  it("every status maps to its own token, not a shared fallback", () => {
    const statuses = ["ok", "degraded", "critical", "info", "unknown"] as const;
    for (const status of statuses) {
      render(<Badge status={status}>{status}</Badge>);
      const badge = screen.getByText(status);
      expect(badge.className).toContain(`status-${status}`);
    }
  });

  it("stale layers a dashed border and reduced opacity onto whatever status/role would otherwise render, rather than becoming its own filled state", () => {
    render(
      <Badge status="degraded" stale>
        Bond down
      </Badge>,
    );
    const badge = screen.getByText("Bond down");
    // The real status's soft wash is still present underneath.
    expect(badge.className).toContain("bg-status-degraded-soft");
    expect(badge.className).toContain("border-dashed");
    expect(badge.className).toContain("border-status-stale");
    expect(badge.className).not.toContain("bg-status-stale");
  });

  it("defaults to comfortable density, byte-for-byte the pre-T-4207 padding", () => {
    render(<Badge status="ok">Up</Badge>);
    const badge = screen.getByText("Up");
    expect(badge.className).toContain("px-2 py-0.5 text-xs");
    expect(badge.dataset.density).toBe("comfortable");
  });

  it("compact density renders tighter padding than comfortable", () => {
    render(
      <Badge status="ok" density="compact">
        Up
      </Badge>,
    );
    const badge = screen.getByText("Up");
    expect(badge.dataset.density).toBe("compact");
    expect(badge.className).toContain("px-1.5 py-0 text-[11px]");
    expect(badge.className).not.toContain("px-2 py-0.5");
  });
});
