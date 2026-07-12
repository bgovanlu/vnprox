import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { MeResponse, SdnTree } from "../api/types";
import { SdnPage } from "./SdnPage";

// T-607: SdnPage now calls useSession() (for its write-trigger capability
// gate — see useSdnWriteGate's doc comment in SdnPage.tsx), so its own
// GET /auth/me needs a real response shape; previously this file's fetch
// mock had no /auth/me branch at all (nothing in SdnPage.tsx called
// useSession() yet) and every request — including /auth/me — fell through
// to the SdnTree fixture body below, which has no `caps` field. Full caps
// here (this file isn't testing the read-only case — readonly-crawl.spec.ts
// and SdnPage's own new "read-only" test below cover that).
const fullCapsMe: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true } },
};

// A small tree covering both of T-401's headline behaviors: a zone with a
// staged-but-unapplied "changed" edit (AC2) and a zone with an error-status
// node (AC4), alongside an in-sync zone for contrast.
const tree: SdnTree = {
  generatedAt: 1_752_000_000,
  zones: [
    {
      id: "vlanz",
      type: "vlan",
      bridge: "vmbr0",
      nodes: ["pve1", "pve2"],
      pending: "changed",
      nodeStatus: [
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "ok" },
      ],
      pendingDiff: {
        state: "changed",
        changedFields: ["mtu"],
        staged: { mtu: 1600 },
        running: { mtu: 1500 },
      },
      vnets: [
        {
          id: "vnet1",
          zone: "vlanz",
          alias: "tenant-net",
          tag: 100,
          subnets: [{ id: "10.0.0.0/24", vnet: "vnet1", cidr: "10.0.0.0/24", gateway: "10.0.0.1" }],
        },
      ],
    },
    {
      id: "simplez",
      type: "simple",
      bridge: "vmbr1",
      nodes: ["pve1", "pve2"],
      nodeStatus: [
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "error", detail: "bridge \"vmbr1\" not found on node \"pve2\"" },
      ],
      vnets: [],
    },
  ],
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SdnPage />
    </QueryClientProvider>,
  );
}

// T-404: SdnPage now also mounts EvpnView on its EVPN/BGP tab, which fires
// its own GET /sdn/evpn/status query — the mock below branches on request
// URL so that query gets a shape-correct (if empty) EvpnStatus rather than
// the SdnTree fixture above (EvpnView.test.tsx covers EVPN content itself
// in depth; this file only needs the tab switch not to crash).
const emptyEvpnStatus = { nodes: [], exitNodes: [], findings: [], generatedAt: 1_752_000_000 };

describe("SdnPage", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) => {
        const body = url.includes("/auth/me") ? fullCapsMe : url.includes("/sdn/evpn/status") ? emptyEvpnStatus : tree;
        return Promise.resolve(
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the zone/vnet/subnet tree and default-selects the first zone", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("vlanz (vlan)")).toBeInTheDocument();
    });
    expect(screen.getByText("vnet1 — tenant-net")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.0/24")).toBeInTheDocument();
    expect(screen.getByText("simplez (simple)")).toBeInTheDocument();

    // Default selection: the first zone's detail is already showing.
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "vlanz" })).toBeInTheDocument();
    });
  });

  it("AC2: a staged-but-unapplied change renders the exact field-level delta", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "vlanz" })).toBeInTheDocument();
    });
    // The changed field name, and both its running (pre-edit) and staged
    // (post-edit) values, render in the diff view.
    expect(screen.getByText("mtu")).toBeInTheDocument();
    expect(screen.getByText("1500")).toBeInTheDocument();
    expect(screen.getByText("1600")).toBeInTheDocument();
  });

  it("AC2: an in-sync entity shows the 'In sync' badge instead of a diff", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("simplez (simple)")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("simplez (simple)"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "simplez" })).toBeInTheDocument();
    });
    expect(screen.getByText("In sync")).toBeInTheDocument();
  });

  it("AC4: per-node status list shows the error detail for the failing node", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("simplez (simple)")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("simplez (simple)"));
    await waitFor(() => {
      expect(screen.getByText("error")).toBeInTheDocument();
    });
    expect(screen.getByText(/bridge "vmbr1" not found on node "pve2"/)).toBeInTheDocument();
  });

  it("selecting a vnet and then a subnet updates the detail panel each time", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("vnet1 — tenant-net")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText("vnet1 — tenant-net"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "vnet1" })).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText("10.0.0.0/24"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "10.0.0.0/24" })).toBeInTheDocument();
    });
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument();
  });

  // T-404: the EVPN/BGP observability view lives alongside the
  // configuration tree in the same SDN cockpit page, switched via a tab —
  // this only checks the tab switch itself renders the EVPN view's own
  // top-level heading; EvpnView.test.tsx covers that view's content in
  // depth against its own fixture.
  it("switching to the EVPN / BGP tab renders the EVPN view instead of the configuration tree", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("vlanz (vlan)")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole("tab", { name: "EVPN / BGP" }));

    await waitFor(() => {
      expect(screen.getByText("Peering matrix")).toBeInTheDocument();
    });
    expect(screen.queryByText("vlanz (vlan)")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("tab", { name: "Configuration" }));
    await waitFor(() => {
      expect(screen.getByText("vlanz (vlan)")).toBeInTheDocument();
    });
  });
});
