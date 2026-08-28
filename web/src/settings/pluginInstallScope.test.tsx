// SPDX-License-Identifier: Apache-2.0

// T-3003 AC3, as a RE-ASSERTION rather than a reimplementation.
//
// The card asks that plugin install show the declared capability scope before
// confirming, and that a scope disagreement between the listing and the
// downloaded manifest be refused and surfaced. Both already hold, and they
// hold in `web/src/hub/` — which this card does not own and must not grow a
// second install path beside. `web/src/hub/HubPage.test.tsx` covers them as
// T-1705 AC4 and T-2104 AC2.
//
// This file exists anyway, deliberately, for one reason: those assertions now
// also guard a claim the *Platform panel* makes. `PluginsSection` tells an
// operator that installing happens in the Hub "where the signature and
// capability-scope gate lives", and points them at it. If that gate ever
// stopped holding, the Platform panel would be sending people somewhere it
// had misdescribed. So the property is asserted from this side of the
// boundary too, by driving the real HubPage — no hub file is modified, and
// no policy is re-implemented here.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import { HubPage } from "../hub/HubPage";
import type { HubEntry, HubInstallResponse } from "../api/types";

const fetchHubIndex = vi.fn();
const installHubItem = vi.fn();

vi.mock("../api/hub", () => ({
  fetchHubIndex: (...args: unknown[]) => fetchHubIndex(...args) as Promise<unknown>,
  installHubItem: (...args: unknown[]) => installHubItem(...args) as Promise<unknown>,
}));

const PLUGIN: HubEntry = {
  type: "plugin",
  id: "acme-tiles",
  name: "Acme Tiles",
  version: "2.0",
  artifactUrl: "/a/acme.json",
  signed: true,
  vetted: true,
  transport: "grpc",
  capabilities: ["netRead", "sdnRead"],
  extensionPoints: ["dashboardTile"],
};

function renderHub(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <HubPage />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchHubIndex.mockReset();
  installHubItem.mockReset();
});

describe("the install path the Platform panel points at", () => {
  it("shows the declared capability scope BEFORE anything is confirmed", async () => {
    fetchHubIndex.mockResolvedValue({ items: [PLUGIN] });
    renderHub();

    await userEvent.click(await screen.findByTestId("hub-tab-plugin"));
    const caps = await screen.findByTestId("hub-caps-acme-tiles");
    expect(caps).toHaveTextContent("netRead");
    expect(caps).toHaveTextContent("sdnRead");
    // Ordering is the property, not just presence: the scope is on screen
    // while the install control is still unpressed.
    expect(installHubItem).not.toHaveBeenCalled();
    expect(screen.getByTestId("hub-install-acme-tiles")).toBeInTheDocument();
  });

  it("refuses a listing/manifest scope disagreement outright, with no trust prompt", async () => {
    // capabilityMismatch is the one status no trust flag overrides: a valid
    // signature proves who produced the manifest, not that it matches the
    // listing the operator consented to.
    fetchHubIndex.mockResolvedValue({ items: [PLUGIN] });
    installHubItem.mockResolvedValueOnce({ type: "plugin", status: "capabilityMismatch" } satisfies HubInstallResponse);

    renderHub();
    await userEvent.click(await screen.findByTestId("hub-tab-plugin"));
    await userEvent.click(await screen.findByTestId("hub-install-acme-tiles"));

    await waitFor(() => {
      expect(installHubItem).toHaveBeenCalledTimes(1);
    });
    expect(await screen.findByText(/capabilities than the catalog showed/i)).toBeInTheDocument();
    // No "trust it anyway" affordance is offered, and nothing re-submits.
    expect(screen.queryByTestId("hub-confirm-trust")).toBeNull();
    expect(installHubItem).toHaveBeenCalledTimes(1);
    const req = installHubItem.mock.calls[0]?.[0] as Record<string, unknown> | undefined;
    expect(req).not.toHaveProperty("trustUnsigned");
    expect(req).not.toHaveProperty("trustNewKey");
  });

  it("does not let a vetted badge skip the trust decision", async () => {
    // "vetted" is informational; the Platform panel's prose says the gate
    // lives here, so this is the gate actually gating.
    fetchHubIndex.mockResolvedValue({ items: [PLUGIN] });
    installHubItem.mockResolvedValueOnce({
      type: "plugin",
      status: "untrustedSignature",
      signer: { fingerprint: "abc123", publicKey: "k" },
    } satisfies HubInstallResponse);

    renderHub();
    await userEvent.click(await screen.findByTestId("hub-tab-plugin"));
    await userEvent.click(await screen.findByTestId("hub-install-acme-tiles"));

    expect(await screen.findByTestId("hub-confirm-trust")).toBeInTheDocument();
    const req = installHubItem.mock.calls[0]?.[0] as Record<string, unknown> | undefined;
    expect(req).not.toHaveProperty("trustNewKey");
  });
});
