import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useTopologyStore } from "../topology/store";
import { TopBar } from "./TopBar";

vi.mock("../api/auth", () => ({
  getMe: vi.fn(() => Promise.resolve({ user: { username: "root", realm: "pam" }, caps: {} })),
  logout: vi.fn(() => Promise.resolve()),
  readCsrfCookie: vi.fn(() => undefined),
}));

function renderTopBar() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <TopBar onOpenHelp={() => undefined} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("TopBar search", () => {
  afterEach(() => {
    useTopologyStore.setState({ spotlightOpen: false });
  });

  it("opens the real spotlight search when the search box is clicked", async () => {
    const user = userEvent.setup();
    useTopologyStore.setState({ spotlightOpen: false });
    renderTopBar();

    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(useTopologyStore.getState().spotlightOpen).toBe(true);
  });
});
