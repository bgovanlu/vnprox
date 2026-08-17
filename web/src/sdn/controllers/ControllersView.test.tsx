// T-3102: ControllersView renders the controller list (type + ASN/peers),
// and the type-conditional create form.
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { MeResponse, SdnTree } from "../../api/types";
import { ToastProvider } from "../../components/Toast";
import { ControllersView } from "./ControllersView";

const fullCapsMe: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

const tree: SdnTree = {
  generatedAt: 1_752_000_000,
  zones: [],
  fabrics: [],
  controllers: [
    { id: "bgp1", type: "bgp", asn: 65000, peers: ["10.0.0.1", "10.0.0.2"] },
    { id: "faucet1", type: "faucet" },
  ],
  prefixLists: [],
  routeMaps: [],
};

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <ControllersView />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

function stubFetch(): void {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = urlOf(input);
      const body = url.includes("/auth/me") ? fullCapsMe : tree;
      return Promise.resolve(
        new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    }),
  );
}

describe("ControllersView", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the controller list with type, ASN, and peers", async () => {
    stubFetch();
    renderView();

    await waitFor(() => {
      expect(screen.getByRole("table", { name: "SDN controllers" })).toBeInTheDocument();
    });

    const table = screen.getByRole("table", { name: "SDN controllers" });
    expect(within(table).getByText("bgp1")).toBeInTheDocument();
    expect(within(table).getByText("BGP")).toBeInTheDocument();
    expect(within(table).getByText("65000")).toBeInTheDocument();
    expect(within(table).getByText("10.0.0.1, 10.0.0.2")).toBeInTheDocument();
    expect(within(table).getByText("faucet1")).toBeInTheDocument();
    expect(within(table).getByText("Faucet")).toBeInTheDocument();
  });

  it("shows an empty state when no controllers are configured", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = urlOf(input);
        const body = url.includes("/auth/me") ? fullCapsMe : { ...tree, controllers: [] };
        return Promise.resolve(
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText("No SDN controllers configured")).toBeInTheDocument();
    });
  });

  it("the create form reveals only the fields for the selected type", async () => {
    stubFetch();
    renderView();

    await waitFor(() => {
      expect(screen.getByRole("table", { name: "SDN controllers" })).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole("button", { name: "+ New controller" }));

    const dialog = await screen.findByRole("dialog");
    // Default type is bgp: ASN/peers show, no EVPN/IS-IS-only fields.
    expect(within(dialog).getByLabelText(/^ASN/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Peers/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Fabric/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^IS-IS domain/)).not.toBeInTheDocument();

    await userEvent.selectOptions(within(dialog).getByLabelText(/^Type/), "evpn");
    expect(within(dialog).getByLabelText(/^Fabric/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Peer group name/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^ASN/)).not.toBeInTheDocument();

    await userEvent.selectOptions(within(dialog).getByLabelText(/^Type/), "isis");
    expect(within(dialog).getByLabelText(/^IS-IS domain/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^IS-IS net/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Fabric/)).not.toBeInTheDocument();

    await userEvent.selectOptions(within(dialog).getByLabelText(/^Type/), "faucet");
    expect(within(dialog).queryByLabelText(/^ASN/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Fabric/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^IS-IS domain/)).not.toBeInTheDocument();
    // Node/Nodes/Loopback are general — shown for every type.
    expect(within(dialog).getByLabelText(/^Loopback/)).toBeInTheDocument();
  });
});
