// T-1706 AC2 (frontend half) + AC3: the embed map renders read-only with
// zero changeset/edit affordances, and an embed token authenticates via a
// bearer header with the session cookie omitted (the ceiling/scoping is
// enforced server-side; here we prove the client never falls back to the
// cookie and never renders a mutation surface). Every backend call is mocked
// at the fetch boundary.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TopologyResponse } from "../api/types";
import { EmbedFrame } from "./EmbedFrame";
import { EmbedMap } from "./EmbedMap";
import { setEmbedToken } from "./embedToken";

const topology: TopologyResponse = {
  nodes: [
    { id: "n1", kind: "node", label: "pve1", layer: "l2", nodeGroup: "pve1", status: "ok", badges: ["mgmt"] },
    { id: "b1", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "degraded", badges: [] },
  ],
  edges: [],
  layers: ["l2"],
  generatedAt: 1,
};

function renderMap(token: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/embed/map?token=${token}`]}>
        <EmbedFrame title="Network map">
          <EmbedMap />
        </EmbedFrame>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  setEmbedToken("");
  vi.restoreAllMocks();
});

describe("EmbedMap", () => {
  it("renders read-only with zero changeset/edit affordances (AC2)", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(topology), { status: 200, headers: { "Content-Type": "application/json" } }),
    );

    renderMap("embed-abc");

    await waitFor(() => {
      expect(screen.getByTestId("embed-map")).toBeInTheDocument();
    });
    // "pve1" appears both as the group heading and the node label.
    expect(screen.getAllByText("pve1").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("vmbr0")).toBeInTheDocument();
    expect(screen.getByTestId("embed-readonly-badge")).toBeInTheDocument();

    // No mutation surface: no buttons, no edit/apply/stage/changeset controls.
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.queryByText(/apply|stage|edit|changeset|delete|create/i)).toBeNull();

    // AC3/AC6 (client side): the embed request carried a bearer token and
    // omitted the session cookie — it never falls back to cookie auth.
    const call = fetchMock.mock.calls[0];
    expect(call).toBeDefined();
    const init = call?.[1] ?? {};
    expect(init.credentials).toBe("omit");
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer embed-abc");
  });

  it("shows a missing-token state when no token is present", () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={["/embed/map"]}>
          <EmbedFrame title="Network map">
            <EmbedMap />
          </EmbedFrame>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByTestId("embed-missing-token")).toBeInTheDocument();
  });
});
