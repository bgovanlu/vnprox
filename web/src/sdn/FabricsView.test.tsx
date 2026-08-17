// T-3101: FabricsView renders the fabric list (protocol + per-node
// membership), the protocol-conditional create form, and the read-only
// prefix-list/route-map tables.
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { MeResponse, SdnTree } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { FabricsView } from "./FabricsView";

const fullCapsMe: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

const tree: SdnTree = {
  generatedAt: 1_752_000_000,
  zones: [],
  fabrics: [
    {
      id: "fab1",
      protocol: "ospf",
      area: "0.0.0.0",
      redistribute: ["connected"],
      nodeStatus: [
        { node: "pve1", status: "ok", detail: "10.255.0.1" },
        { node: "pve2", status: "ok", detail: "10.255.0.2" },
      ],
    },
    {
      id: "wgfab",
      protocol: "wireguard",
      persistentKeepalive: 25,
      nodeStatus: [],
    },
  ],
  prefixLists: [{ id: "pl1" }],
  routeMaps: [{ id: "rm1" }],
};

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <FabricsView />
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

describe("FabricsView", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the fabric list with protocol and per-node membership, plus the read-only prefix-list/route-map tables", async () => {
    stubFetch();
    renderView();

    await waitFor(() => {
      expect(screen.getByRole("table", { name: "SDN fabrics" })).toBeInTheDocument();
    });

    const fabricsTable = screen.getByRole("table", { name: "SDN fabrics" });
    expect(within(fabricsTable).getByText("fab1")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("OSPF")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("pve1")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("pve2")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("wgfab")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("WireGuard")).toBeInTheDocument();
    expect(within(fabricsTable).getByText("no member nodes reported")).toBeInTheDocument();

    const prefixListsTable = screen.getByRole("table", { name: "SDN prefix-lists" });
    expect(within(prefixListsTable).getByText("pl1")).toBeInTheDocument();
    const routeMapsTable = screen.getByRole("table", { name: "SDN route-maps" });
    expect(within(routeMapsTable).getByText("rm1")).toBeInTheDocument();
  });

  it("shows an empty state when no fabrics are configured", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = urlOf(input);
        const body = url.includes("/auth/me") ? fullCapsMe : { ...tree, fabrics: [], prefixLists: [], routeMaps: [] };
        return Promise.resolve(
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText("No SDN fabrics configured")).toBeInTheDocument();
    });
    expect(screen.getByText("No prefix-lists configured")).toBeInTheDocument();
    expect(screen.getByText("No route-maps configured")).toBeInTheDocument();
  });

  it("the create form reveals only the fields for the selected protocol", async () => {
    stubFetch();
    renderView();

    await waitFor(() => {
      expect(screen.getByRole("table", { name: "SDN fabrics" })).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole("button", { name: "+ New fabric" }));

    const dialog = await screen.findByRole("dialog");
    // Field's label + help text are both inside the <label> element, so the
    // accessible name getByLabelText matches against is "<label> <help>"
    // concatenated — every lookup below anchors on just the label word
    // itself (^...) rather than an exact full-string match.
    // Default protocol is bgp: only Redistribute shows, no OSPF/OpenFabric/
    // WireGuard-only fields.
    expect(within(dialog).getByLabelText(/^Redistribute/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Area/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^CSNP interval/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Persistent keepalive/)).not.toBeInTheDocument();

    await userEvent.selectOptions(within(dialog).getByLabelText(/^Protocol/), "openfabric");
    expect(within(dialog).getByLabelText(/^CSNP interval/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Hello interval/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Route filter/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Area/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Redistribute/)).not.toBeInTheDocument();

    await userEvent.selectOptions(within(dialog).getByLabelText(/^Protocol/), "wireguard");
    expect(within(dialog).getByLabelText(/^Persistent keepalive/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^Route filter/)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/^CSNP interval/)).not.toBeInTheDocument();
  });
});
