// SPDX-License-Identifier: Apache-2.0

// T-1706 AC5 (frontend half): the posture embed renders the read-only score
// when available and the documented "not yet available" state on a 404.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Posture } from "../api/posture";
import { EmbedFrame } from "./EmbedFrame";
import { EmbedPosture } from "./EmbedPosture";
import { setEmbedToken } from "./embedToken";

const posture: Posture = {
  overall: 72,
  qualified: true,
  computedAt: 1_700_000_000,
  factors: [
    { name: "Exposed ports", detail: "3 exposed", value: 3, contribution: 20, weight: 3, scorePct: 70, evaluated: true },
    { name: "SPOF", detail: "not assessable", caveat: "no quorum config", value: 0, contribution: 0, weight: 2, scorePct: -1, evaluated: false },
  ],
};

function renderPosture() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/embed/posture?token=embed-abc"]}>
        <EmbedFrame title="Network posture">
          <EmbedPosture />
        </EmbedFrame>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  setEmbedToken("");
  vi.restoreAllMocks();
});

describe("EmbedPosture", () => {
  it("renders the read-only score and factor breakdown", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(posture), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    renderPosture();
    await waitFor(() => {
      expect(screen.getByTestId("embed-posture")).toBeInTheDocument();
    });
    expect(screen.getByText("72")).toBeInTheDocument();
    expect(screen.getByText("Exposed ports")).toBeInTheDocument();
    expect(screen.getByText("Not evaluated")).toBeInTheDocument();
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  it("renders the documented not-available state on a 404 (AC5)", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "not_found", message: "no posture score computed yet" } }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    renderPosture();
    await waitFor(() => {
      expect(document.querySelector('[data-embed-state="posture-unavailable"]')).not.toBeNull();
    });
  });
});
