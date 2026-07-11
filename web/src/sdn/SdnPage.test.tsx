import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { SdnTree } from "../api/types";
import { SdnPage } from "./SdnPage";

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

describe("SdnPage", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify(tree), { status: 200, headers: { "Content-Type": "application/json" } }),
        ),
      ),
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
});
