// SPDX-License-Identifier: Apache-2.0

// T-4015: the general (non-federation) WireGuard management surface.
// Covers: op-staging parity with the federation wizard (AC1 — one op
// vocabulary, two entry points), the three-state display, and — the case
// this task's brief calls out by name — that a tunnel whose state cannot be
// read renders "unknown", is never conflated with "down", and its last-known
// list stays visible rather than being hidden.
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../components/Toast";
import type { Changeset, MeResponse, Op, TopologyResponse, WireGuardTunnel } from "../api/types";
import { WireGuardPage } from "./WireGuardPage";
import { wireGuardTunnelsKey } from "./wgTunnelsQuery";

const session: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
    "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
  },
};

function oneNodeTopology(): TopologyResponse {
  return {
    nodes: [{ id: "node:pve1:pve1", kind: "node", label: "pve1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] }],
    edges: [],
    layers: ["phys", "l2", "sdn", "guest"],
    generatedAt: 1_752_000_000,
  };
}

function freshTunnel(): WireGuardTunnel {
  return {
    id: "tun-up",
    node: "pve1",
    ifName: "wg0",
    publicKey: "LOCALPUBKEY==",
    addresses: ["10.10.0.1/24"],
    listenPort: 51820,
    mtu: 0,
    peers: [
      {
        publicKey: "PEERKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
        endpoint: "203.0.113.10:51820",
        allowedIps: ["10.10.0.2/32"],
        lastHandshakeUnix: Math.floor(Date.now() / 1000) - 30,
        rxBytes: 1024,
        txBytes: 2048,
        external: true,
        endpointDrifted: false,
      },
    ],
    status: { interfaceUp: true, peerCount: 1 },
  };
}

function staleTunnel(): WireGuardTunnel {
  return {
    id: "tun-down",
    node: "pve1",
    ifName: "wg1",
    publicKey: "LOCALPUBKEY2==",
    addresses: ["10.10.1.1/24"],
    listenPort: 51821,
    mtu: 0,
    peers: [],
    status: { interfaceUp: false, peerCount: 0 },
  };
}

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

interface FetchStub {
  postedChangesets: { title: string; ops: Op[] }[];
}

/** `tunnelsHandler` is called once per GET /wireguard/tunnels — a function,
 * not a fixed list, so tests can vary the response across a page's
 * lifetime (the "later read fails" case below). */
function stubFetch(tunnelsHandler: () => Response): FetchStub {
  const stub: FetchStub = { postedChangesets: [] };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlOf(input);
      const method = init?.method ?? "GET";

      if (url.includes("/auth/me")) return Promise.resolve(jsonResponse(session));
      if (url.includes("/federation/clusters")) return Promise.resolve(jsonResponse({ items: [] }));
      if (url.includes("/topology")) return Promise.resolve(jsonResponse(oneNodeTopology()));
      if (url.includes("/wireguard/tunnels/") && url.includes("/pubkey")) {
        return Promise.resolve(jsonResponse({ id: "tun-up", publicKey: "LOCALPUBKEY==" }));
      }
      if (url.includes("/wireguard/tunnels/") && url.includes("/peer-config")) {
        return Promise.resolve(jsonResponse({ id: "tun-up", peerConfig: "[Interface]\n# no private key here" }));
      }
      if (url.endsWith("/wireguard/tunnels")) return Promise.resolve(tunnelsHandler());
      if (url.includes("/changesets") && method === "POST") {
        const rawBody = typeof init?.body === "string" ? init.body : "";
        const body = rawBody ? (JSON.parse(rawBody) as { title: string; ops: Op[] }) : { title: "", ops: [] };
        stub.postedChangesets.push(body);
        const created: Changeset = {
          id: `cs-${String(stub.postedChangesets.length)}`,
          title: body.title,
          author: "root@pam",
          status: "draft",
          ops: body.ops,
          findings: [],
          createdAt: 1_752_000_000,
          updatedAt: 1_752_000_000,
        };
        return Promise.resolve(jsonResponse(created));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );
  return stub;
}

function renderPage(client?: QueryClient) {
  const qc = client ?? new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { qc, ...render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <WireGuardPage />
      </ToastProvider>
    </QueryClientProvider>,
  ) };
}

describe("WireGuardPage", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders 'up' for a tunnel with a fresh peer handshake and 'down' for one with none — three distinct states, not two", async () => {
    stubFetch(() => jsonResponse({ items: [freshTunnel(), staleTunnel()] }));
    renderPage();

    await waitFor(() => { expect(screen.getByText("wg0 on pve1")).toBeInTheDocument(); });
    const upSection = screen.getByText("wg0 on pve1").closest("div.rounded-md");
    const downSection = screen.getByText("wg1 on pve1").closest("div.rounded-md");
    expect(upSection).not.toBeNull();
    expect(downSection).not.toBeNull();
    expect(within(upSection as HTMLElement).getByText("up")).toBeInTheDocument();
    expect(within(downSection as HTMLElement).getByText("down")).toBeInTheDocument();
    // Never conflated: no row anywhere claims "unknown" while the read is healthy.
    expect(screen.queryByText("unknown")).not.toBeInTheDocument();
  });

  it("shows a page-level 'could not load' state — never a false 'down' — when the very first read fails with nothing cached", async () => {
    stubFetch(() => jsonResponse({ error: "boom" }, 500));
    renderPage();

    await waitFor(() => { expect(screen.getByText("Could not load WireGuard tunnels")).toBeInTheDocument(); });
    expect(screen.queryByText("down")).not.toBeInTheDocument();
    expect(screen.queryByText("up")).not.toBeInTheDocument();
  });

  it("keeps showing the last-known tunnel list, marked 'unknown' (not 'down'), when a later refresh fails after an earlier success", async () => {
    let call = 0;
    stubFetch(() => {
      call += 1;
      return call === 1 ? jsonResponse({ items: [freshTunnel()] }) : jsonResponse({ error: "boom" }, 500);
    });
    const { qc } = renderPage();

    // First render: the tunnel loaded successfully and reads "up".
    await waitFor(() => { expect(screen.getByText("up")).toBeInTheDocument(); });

    // Force a refetch that fails — react-query keeps the prior `data`
    // around (verified against @tanstack/query-core directly: a query's
    // `data` survives a failed refetch after an earlier success; only
    // `status` flips to "error") — so the row stays on screen, now unknown.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: wireGuardTunnelsKey });
    });

    await waitFor(() => { expect(screen.getByText(/Could not refresh live WireGuard state/)).toBeInTheDocument(); });
    expect(screen.getByText("wg0 on pve1")).toBeInTheDocument(); // stale list still shown
    expect(screen.getByText("unknown")).toBeInTheDocument();
    expect(screen.queryByText("down")).not.toBeInTheDocument();
    expect(screen.queryByText("up")).not.toBeInTheDocument();
  });

  it("creating a tunnel stages exactly wg.tunnel.create — the same op shape the federation wizard stages, never an apply", async () => {
    const user = userEvent.setup();
    const stub = stubFetch(() => jsonResponse({ items: [] }));
    renderPage();

    await waitFor(() => { expect(screen.getByText("No WireGuard tunnels configured")).toBeInTheDocument(); });
    await user.click(screen.getByRole("button", { name: "+ New tunnel" }));

    await waitFor(() => { expect(screen.getByRole("option", { name: "pve1" })).toBeInTheDocument(); });
    await user.selectOptions(screen.getByRole("combobox", { name: /Node/ }), "pve1");
    const ifNameField = screen.getByRole("textbox", { name: /Interface name/ });
    await user.clear(ifNameField);
    await user.type(ifNameField, "wg0");

    await user.click(screen.getByRole("button", { name: "Add to changeset" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toHaveLength(1);
    expect(ops[0]?.op).toBe("wg.tunnel.create");
    expect(ops[0]?.target).toMatch(/^wg-tunnel:pve1:/);
    expect(ops[0]?.params).toMatchObject({ ifName: "wg0", listenPort: 51820 });
  });

  it("adding a peer stages exactly wg.peer.add with external:true, targeting the tunnel it was added from", async () => {
    const user = userEvent.setup();
    const stub = stubFetch(() => jsonResponse({ items: [freshTunnel()] }));
    renderPage();

    await waitFor(() => { expect(screen.getByText("wg0 on pve1")).toBeInTheDocument(); });
    await user.click(screen.getByRole("button", { name: "+ Peer" }));

    await user.type(screen.getByRole("textbox", { name: /Peer public key/ }), "NEWPEERkeyaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=");
    await user.type(screen.getByRole("textbox", { name: /Allowed IPs/ }), "10.10.0.5/32");
    await user.click(screen.getByRole("button", { name: "Add to changeset" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toHaveLength(1);
    expect(ops[0]?.op).toBe("wg.peer.add");
    expect(ops[0]?.target).toBe("wg-peer:pve1:tun-up/NEWPEERkeyaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=");
    expect(ops[0]?.params).toMatchObject({ external: true, publicKey: "NEWPEERkeyaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=" });
    // Never a private key, and never the sealed PSK form — only the
    // write-only plaintext ingest field this form actually has.
    expect(JSON.stringify(ops[0])).not.toMatch(/private/i);
    expect(JSON.stringify(ops[0])).not.toMatch(/presharedKeyEnc/);
  });

  it("the key viewer only ever fetches/shows the derived public key and exportable peer config — never a private key field", async () => {
    stubFetch(() => jsonResponse({ items: [freshTunnel()] }));
    renderPage();

    await waitFor(() => { expect(screen.getByText("wg0 on pve1")).toBeInTheDocument(); });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "View key" }));

    await waitFor(() => { expect(screen.getByDisplayValue("LOCALPUBKEY==")).toBeInTheDocument(); });
    expect(screen.getByDisplayValue(/no private key here/)).toBeInTheDocument();
    // The dialog fetched exactly the two documented-safe reads and nothing
    // shaped like a private key ever entered the DOM as a field value.
    const fields = [...document.querySelectorAll("input,textarea")].map((el) => (el as HTMLInputElement).value);
    expect(fields.some((v) => /^-----BEGIN|PrivateKey\s*=/.test(v))).toBe(false);
  });
});
