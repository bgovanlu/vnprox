// T-1705 UI tests: the hub browse/install page surfaces a plugin's declared
// capabilities before install (AC4), shows the informational "vetted" badge,
// and never bypasses the trust decision — a vetted-but-untrusted or unsigned
// entry still routes through the explicit trust confirm (AC5).
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import { HubPage } from "./HubPage";
import type { HubEntry, HubInstallResponse } from "../api/types";

const blueprintEntry: HubEntry = {
  type: "blueprint",
  id: "vetted-bp",
  name: "Vetted BP",
  version: "1.0",
  artifactUrl: "/a/vetted-bp.json",
  signerFingerprint: "abc123",
  signed: true,
  vetted: true,
};

const pluginEntry: HubEntry = {
  type: "plugin",
  id: "acme-tiles",
  name: "Acme Tiles",
  version: "2.0",
  artifactUrl: "/a/acme.json",
  signed: true,
  vetted: false,
  transport: "grpc",
  capabilities: ["netRead"],
  extensionPoints: ["dashboardTile"],
};

const fetchHubIndex = vi.fn();
const installHubItem = vi.fn();

vi.mock("../api/hub", () => ({
  fetchHubIndex: (...args: unknown[]) => fetchHubIndex(...args) as Promise<unknown>,
  installHubItem: (...args: unknown[]) => installHubItem(...args) as Promise<unknown>,
}));

function renderPage(): void {
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

describe("HubPage", () => {
  it("shows the vetted badge and the blueprint list", async () => {
    fetchHubIndex.mockResolvedValue({ items: [blueprintEntry] });
    renderPage();
    expect(await screen.findByTestId("hub-entry-vetted-bp")).toBeInTheDocument();
    expect(screen.getByTestId("hub-vetted-vetted-bp")).toHaveTextContent("vetted");
  });

  // T-3709: the badge is never just the bare word "vetted" — hovering it
  // explains, in plain terms, that this is an automated-hygiene result and
  // NOT a human review, an endorsement, or a reproducible-build claim. This
  // is the badge-copy deliverable itself, so it gets its own assertion
  // rather than riding along inside an unrelated test.
  it("explains the vetted badge on hover as automated checks, not endorsement", async () => {
    fetchHubIndex.mockResolvedValue({ items: [blueprintEntry] });
    renderPage();
    const badge = await screen.findByTestId("hub-vetted-vetted-bp");
    await userEvent.hover(badge);
    const explanation = await screen.findByRole("tooltip", {}, { timeout: 2000 });
    expect(explanation).toHaveTextContent(/passed automated checks only/i);
    expect(explanation).toHaveTextContent(/not a human review/i);
    expect(explanation).toHaveTextContent(/not.*endorsement/i);
    expect(explanation).toHaveTextContent(/not proof of a reproducible build/i);
  });

  it("surfaces a plugin's declared capabilities before install (AC4)", async () => {
    fetchHubIndex.mockResolvedValue({ items: [pluginEntry] });
    renderPage();
    // Switch to the plugin tab.
    await userEvent.click(await screen.findByTestId("hub-tab-plugin"));
    const caps = await screen.findByTestId("hub-caps-acme-tiles");
    expect(caps).toHaveTextContent("netRead");
  });

  it("routes a vetted-but-untrusted install through the explicit trust step (AC5)", async () => {
    fetchHubIndex.mockResolvedValue({ items: [blueprintEntry] });
    // First install attempt returns untrustedSignature — vetted did NOT bypass.
    const untrusted: HubInstallResponse = { type: "blueprint", status: "untrustedSignature", signer: { fingerprint: "abc123", publicKey: "k" } };
    const imported: HubInstallResponse = { type: "blueprint", status: "imported" };
    installHubItem.mockResolvedValueOnce(untrusted).mockResolvedValueOnce(imported);

    renderPage();
    await userEvent.click(await screen.findByTestId("hub-install-vetted-bp"));

    // A trust confirmation appears instead of a silent install.
    const confirm = await screen.findByTestId("hub-confirm-trust");
    expect(installHubItem).toHaveBeenCalledTimes(1);
    const firstArg = installHubItem.mock.calls[0]?.[0] as Record<string, unknown> | undefined;
    expect(firstArg).toMatchObject({ type: "blueprint", id: "vetted-bp" });
    expect(firstArg).not.toHaveProperty("trustNewKey");

    // Confirming re-submits with the explicit trustNewKey flag.
    await userEvent.click(confirm);
    await waitFor(() => { expect(installHubItem).toHaveBeenCalledTimes(2); });
    expect(installHubItem.mock.calls[1]?.[0]).toMatchObject({ type: "blueprint", id: "vetted-bp", trustNewKey: true });
  });

  it("routes an unsigned install through a trustUnsigned confirm", async () => {
    const unsignedEntry: HubEntry = { ...blueprintEntry, id: "u", name: "U", signed: false, vetted: false, signerFingerprint: undefined };
    fetchHubIndex.mockResolvedValue({ items: [unsignedEntry] });
    installHubItem
      .mockResolvedValueOnce({ type: "blueprint", status: "unsigned" } satisfies HubInstallResponse)
      .mockResolvedValueOnce({ type: "blueprint", status: "imported" } satisfies HubInstallResponse);

    renderPage();
    await userEvent.click(await screen.findByTestId("hub-install-u"));
    await userEvent.click(await screen.findByTestId("hub-confirm-trust"));
    await waitFor(() => { expect(installHubItem).toHaveBeenCalledTimes(2); });
    expect(installHubItem.mock.calls[1]?.[0]).toMatchObject({ trustUnsigned: true });
  });

  // T-2104 AC2: a capability mismatch is a hard refusal with no trust
  // dialog offered — there is no flag that makes "the catalog showed you
  // something other than what this would grant" installable.
  it("surfaces a capability mismatch as a refusal, never a trust prompt", async () => {
    fetchHubIndex.mockResolvedValue({ items: [pluginEntry] });
    installHubItem.mockResolvedValueOnce({ type: "plugin", status: "capabilityMismatch" } satisfies HubInstallResponse);

    renderPage();
    await userEvent.click(await screen.findByTestId("hub-tab-plugin"));
    await userEvent.click(await screen.findByTestId("hub-install-acme-tiles"));

    await waitFor(() => { expect(installHubItem).toHaveBeenCalledTimes(1); });
    expect(screen.queryByTestId("hub-confirm-trust")).not.toBeInTheDocument();
    expect(await screen.findByText(/capabilities than the catalog showed/i)).toBeInTheDocument();
  });
});
