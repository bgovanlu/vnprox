// T-904 AC1: "NavRail's Home entry highlights when active." Mirrors
// TopBar.test.tsx's render setup; findings/queries.ts's useFindingsQuery
// (behind the Tools nav item's count badge) is mocked at the api boundary
// so this never issues a real fetch.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { NavRail } from "./NavRail";

vi.mock("../api/findings", () => ({
  fetchFindings: () => Promise.resolve([]),
  fixFinding: vi.fn(),
}));

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="*" element={<NavRail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("NavRail Home entry", () => {
  it("highlights Home at the index route", () => {
    renderAt("/");
    const home = screen.getByRole("link", { name: /Home/ });
    expect(home.className).toContain("bg-accent-600/10");
  });

  it("does not highlight Home (or leave every other item active) on another route", () => {
    renderAt("/topology");
    const home = screen.getByRole("link", { name: /Home/ });
    expect(home.className).not.toContain("bg-accent-600/10");
    const topology = screen.getByRole("link", { name: /Topology/ });
    expect(topology.className).toContain("bg-accent-600/10");
  });
});
