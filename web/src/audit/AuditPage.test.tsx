// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditPage } from "./AuditPage";
import { parseDateInput, toAuditFilter } from "./filters";
import { auditQueryString } from "../api/audit";
import type { AuditListResponse } from "../api/audit";

describe("toAuditFilter / parseDateInput / auditQueryString", () => {
  it("drops empty fields and trims values", () => {
    const filter = toAuditFilter({ user: "  root@pam ", result: "", target: "", from: "", to: "" });
    expect(filter).toEqual({ user: "root@pam", result: undefined, target: undefined, from: undefined, to: undefined });
  });

  it("parses datetime-local values into unix seconds", () => {
    const secs = parseDateInput("2026-07-10T12:00");
    expect(secs).toBe(Math.floor(new Date("2026-07-10T12:00").getTime() / 1000));
    expect(parseDateInput("")).toBeUndefined();
    expect(parseDateInput("not-a-date")).toBeUndefined();
  });

  it("builds the documented query string (filters ANDed, cursor passthrough)", () => {
    const qs = auditQueryString({ user: "alice", result: "failed", from: 100, to: 200 }, "150:3", 25);
    const params = new URLSearchParams(qs);
    expect(params.get("limit")).toBe("25");
    expect(params.get("user")).toBe("alice");
    expect(params.get("result")).toBe("failed");
    expect(params.get("from")).toBe("100");
    expect(params.get("to")).toBe("200");
    expect(params.get("cursor")).toBe("150:3");
    expect(params.get("target")).toBeNull();
  });
});

const page: AuditListResponse = {
  items: [
    {
      id: 2,
      at: 1_752_000_100,
      username: "alice@pve",
      action: "changeset.apply",
      result: "awaiting_confirm",
      changesetId: "01CS",
      detail: { stepCount: 3 },
    },
    { id: 1, at: 1_752_000_000, username: "bob@pve", action: "changeset.rollback", result: "rolled_back" },
  ],
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AuditPage />
    </QueryClientProvider>,
  );
}

describe("AuditPage", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify(page), { status: 200, headers: { "Content-Type": "application/json" } }),
        ),
      ),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders audit rows and expands one to its detail", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("alice@pve")).toBeInTheDocument();
    });
    expect(screen.getByText("bob@pve")).toBeInTheDocument();
    expect(screen.getByText("changeset.apply")).toBeInTheDocument();

    await userEvent.click(screen.getByText("alice@pve"));
    await waitFor(() => {
      expect(screen.getByText(/"stepCount": 3/)).toBeInTheDocument();
    });
  });

  it("re-queries with the entered filters on submit", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("alice@pve")).toBeInTheDocument();
    });

    await userEvent.type(screen.getByLabelText("User"), "alice@pve");
    await userEvent.click(screen.getByRole("button", { name: "Filter" }));

    const fetchMock = vi.mocked(fetch);
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => (typeof c[0] === "string" ? c[0] : ""));
      expect(calls.some((url) => url.includes("user=alice%40pve"))).toBe(true);
    });
  });
});
