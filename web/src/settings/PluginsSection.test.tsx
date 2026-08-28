// SPDX-License-Identifier: Apache-2.0

// T-3003: plugin lifecycle, the capability ceiling, and — the part that is
// easy to get wrong — "the registry is not wired here" rendered as something
// other than "no plugins are installed".
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { Plugin } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { PluginsSection } from "./PluginsSection";

const fetchPlugins = vi.fn<() => Promise<Plugin[]>>();
const enablePlugin = vi.fn<(id: string) => Promise<void>>();
const disablePlugin = vi.fn<(id: string) => Promise<void>>();
const uninstallPlugin = vi.fn<(id: string) => Promise<void>>();

vi.mock("../api/plugins", () => ({
  fetchPlugins: () => fetchPlugins(),
  enablePlugin: (id: string) => enablePlugin(id),
  disablePlugin: (id: string) => disablePlugin(id),
  uninstallPlugin: (id: string) => uninstallPlugin(id),
}));

const TILES: Plugin = {
  id: "acme-tiles",
  name: "Acme Tiles",
  version: "1.2.0",
  apiVersion: "v1",
  transport: "grpc",
  extensionPoints: ["dashboardTile"],
  capabilities: ["netRead"],
  installedBy: "root@pam",
  installedAt: 1_700_000_000,
  enabled: true,
};

function renderSection(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <PluginsSection />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  fetchPlugins.mockReset();
  enablePlugin.mockReset();
  disablePlugin.mockReset();
  uninstallPlugin.mockReset();
});

describe("the capability ceiling", () => {
  it("shows the declared scope and names it as a ceiling", async () => {
    fetchPlugins.mockResolvedValue([TILES]);
    renderSection();

    const caps = await screen.findByTestId("plugin-caps-acme-tiles");
    expect(caps).toHaveTextContent("netRead");
    expect(caps).toHaveTextContent(/maximum this plugin may touch/i);
    expect(caps).toHaveTextContent(/never itself a way to apply one/i);
  });

  it("says 'declares no capabilities' rather than leaving the cell blank", async () => {
    fetchPlugins.mockResolvedValue([{ ...TILES, capabilities: [] }]);
    renderSection();

    expect(await screen.findByTestId("plugin-caps-acme-tiles")).toHaveTextContent("declares no capabilities");
  });

  it("repeats the scope in the uninstall confirmation", async () => {
    const user = userEvent.setup();
    fetchPlugins.mockResolvedValue([TILES]);
    renderSection();

    await user.click(await screen.findByTestId("plugin-uninstall-acme-tiles"));
    expect(await screen.findByText(/declared capability scope was netRead/i)).toBeInTheDocument();
  });
});

describe("there is no install path here", () => {
  it("points at the Hub instead of offering one", async () => {
    fetchPlugins.mockResolvedValue([]);
    renderSection();

    await screen.findByTestId("plugins-empty");
    expect(screen.getByRole("link", { name: "Hub" })).toHaveAttribute("href", "/hub");
    // No install control of any kind. (`{ name: /^install$/i }` rather than a
    // loose match, so the help anchor — whose accessible name happens to
    // contain the word "Installed" — is not mistaken for one.)
    expect(screen.queryByRole("button", { name: /^install$/i })).toBeNull();
  });
});

describe("absent vs unknown", () => {
  it("distinguishes 'no plugins installed' from 'the registry is not wired'", async () => {
    fetchPlugins.mockResolvedValue([]);
    renderSection();
    const empty = await screen.findByTestId("plugins-empty");
    expect(empty).toHaveTextContent("wired and reports no installed plugins");
  });

  it("renders a 404 as an unanswerable question, not as an empty registry", async () => {
    fetchPlugins.mockRejectedValue(new ApiError(404, "not_found", "no such API route"));
    renderSection();

    const notice = await screen.findByTestId("plugins-error");
    expect(notice).toHaveAttribute("data-refusal-kind", "unavailable");
    expect(notice).toHaveTextContent(/different from having no plugins installed/i);
    expect(screen.queryByTestId("plugins-empty")).toBeNull();
  });

  it("renders a 403 as a capability refusal", async () => {
    fetchPlugins.mockRejectedValue(new ApiError(403, "forbidden", "missing capability: netRead"));
    renderSection();

    expect(await screen.findByTestId("plugins-error")).toHaveAttribute("data-refusal-kind", "forbidden");
  });
});

describe("lifecycle", () => {
  it("disables an enabled plugin", async () => {
    const user = userEvent.setup();
    fetchPlugins.mockResolvedValue([TILES]);
    renderSection();

    const toggle = await screen.findByTestId("plugin-toggle-acme-tiles");
    expect(toggle).toHaveTextContent("Disable");
    await user.click(toggle);
    expect(disablePlugin).toHaveBeenCalledWith("acme-tiles");
    expect(enablePlugin).not.toHaveBeenCalled();
  });

  it("enables a disabled plugin", async () => {
    const user = userEvent.setup();
    fetchPlugins.mockResolvedValue([{ ...TILES, enabled: false }]);
    renderSection();

    expect(await screen.findByTestId("plugin-state-acme-tiles")).toHaveAttribute("data-enabled", "false");
    const toggle = screen.getByTestId("plugin-toggle-acme-tiles");
    expect(toggle).toHaveTextContent("Enable");
    await user.click(toggle);
    expect(enablePlugin).toHaveBeenCalledWith("acme-tiles");
    expect(disablePlugin).not.toHaveBeenCalled();
  });

  it("requires a confirmation before uninstalling", async () => {
    const user = userEvent.setup();
    fetchPlugins.mockResolvedValue([TILES]);
    renderSection();

    await user.click(await screen.findByTestId("plugin-uninstall-acme-tiles"));
    expect(uninstallPlugin).not.toHaveBeenCalled();

    await user.click(screen.getByTestId("plugin-uninstall-confirm"));
    expect(uninstallPlugin).toHaveBeenCalledWith("acme-tiles");
  });
});
