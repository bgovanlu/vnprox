// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InstanceConfigResponse, MeResponse, ProtectedInterfacesStatusResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { useThemeStore } from "../store/theme";
import { SettingsPage } from "./SettingsPage";

const me: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: false, fwRead: true, fwWrite: false, guestNet: false, audit: true, capture: true } },
};

const config: InstanceConfigResponse = {
  version: "9.9.9-test",
  listen: "0.0.0.0:8007",
  pveApiUrl: "https://127.0.0.1:8006",
  protectedPath: "/etc/pve/vnprox/protected.json",
  pveInterval: "10s",
  hostInterval: "5s",
  lldpInterval: "30s",
  confirmTimeoutDefaultSec: 120,
  snapshotKeepDays: 90,
  snapshotPinDays: 7,
  readOnly: true,
  demo: false,
  allowDangerousOps: false,
};

const status: ProtectedInterfacesStatusResponse = { source: "confirmed", nodes: {} };

vi.mock("../api/config", () => ({ fetchInstanceConfig: vi.fn(() => Promise.resolve(config)) }));
vi.mock("../api/auth", () => ({ getMe: vi.fn(() => Promise.resolve(me)), logout: vi.fn(() => Promise.resolve()) }));
vi.mock("../api/protectedInterfaces", () => ({
  fetchMgmtStatus: vi.fn(() => Promise.resolve(status)),
  fetchProtectedInterfaces: vi.fn(() => Promise.resolve({ nodes: {}, updatedAt: 0, version: 0 })),
  fetchProtectedInterfacesSuggest: vi.fn(() => Promise.resolve({ nodes: {} })),
  saveProtectedInterfaces: vi.fn(),
}));
vi.mock("../api/onboarding", () => ({
  fetchOnboardingProgress: vi.fn(() => Promise.reject(Object.assign(new Error("not found"), { status: 404 }))),
  saveOnboardingProgress: vi.fn(),
}));

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ToastProvider>
          <SettingsPage />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("SettingsPage", () => {
  afterEach(() => {
    useThemeStore.setState({ theme: "dark" });
  });

  it("renders account, instance config, and protected-interface status", async () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "Settings" })).toBeInTheDocument();
    expect(await screen.findByText("root@pam")).toBeInTheDocument();
    // Instance section (read-only config)
    expect(await screen.findByText("9.9.9-test")).toBeInTheDocument();
    expect(screen.getByText("Read-only mode")).toBeInTheDocument();
    expect(screen.getByText("120s")).toBeInTheDocument();
    // Cluster & safety
    expect(await screen.findByText(/Confirmed during onboarding/)).toBeInTheDocument();
    // Capability summary reflects the session (audit granted, fwWrite not)
    expect(screen.getByText("Change network")).toBeInTheDocument();
    expect(screen.queryByText("Change firewall")).not.toBeInTheDocument();
  });

  it("changes the theme from the appearance control", async () => {
    const user = userEvent.setup();
    useThemeStore.setState({ theme: "dark" });
    renderPage();
    await user.click(screen.getByRole("radio", { name: "light" }));
    expect(useThemeStore.getState().theme).toBe("light");
  });
});
