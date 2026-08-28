// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../../components/Toast";
import type { IpamSubnetsResponse, MeResponse } from "../../api/types";
import { BridgeEditor } from "./BridgeEditor";

// T-405 acceptance criterion 4's proof-of-interface test: NextFreePicker
// (web/src/ipam/NextFreePicker.tsx), the same shared component the IPAM
// page's reserve dialog uses, is a real, working integration inside this
// unrelated form — not just an unused export.
const subnets: IpamSubnetsResponse = {
  generatedAt: 1_752_000_000,
  items: [
    {
      cidr: "10.50.0.0/24",
      zone: "labz",
      vnet: "vnet10",
      gateway: "10.50.0.1",
      source: "sdn",
      total: 254,
      allocated: 1,
      observed: 0,
      conflicts: 0,
      utilization: 0.004,
    },
  ],
};

const me: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

function renderEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <BridgeEditor open onOpenChange={() => undefined} node="pve1" newBridgeId="vmbr9" candidatePorts={[]} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("BridgeEditor + NextFreePicker integration (T-405 AC4)", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
        if (url.includes("/auth/me")) {
          return Promise.resolve(new Response(JSON.stringify(me), { status: 200, headers: { "Content-Type": "application/json" } }));
        }
        if (url.includes("/ipam/subnets/") && url.includes("/allocations")) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                cidr: "10.50.0.0/24",
                prefix: 24,
                total: 254,
                conflicts: [],
                generatedAt: 1_752_000_000,
                entries: [{ ip: "10.50.0.1", state: "gateway" }],
                freeRanges: [{ start: "10.50.0.2", end: "10.50.0.254", count: 253 }],
                counts: { allocated: 0, reserved: 0, observed: 0, gateway: 1, conflict: 0, free: 253 },
              }),
              { status: 200, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
        if (url.includes("/ipam/subnets")) {
          return Promise.resolve(new Response(JSON.stringify(subnets), { status: 200, headers: { "Content-Type": "application/json" } }));
        }
        return Promise.resolve(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("suggests and appends a free address from a chosen SDN subnet into the Addresses field", async () => {
    renderEditor();

    // The subnet picker BridgeEditor now renders (BridgeAddressSuggest)
    // lists the fetched SDN subnet.
    await waitFor(() => {
      expect(screen.getByText("10.50.0.0/24")).toBeInTheDocument();
    });

    // BridgeEditor's own Kind selector (Linux/OVS) is also a combobox as of
    // the T-402/T-502 merge, so target this one by its accessible name
    // rather than assuming it's the only one on the page.
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Suggest address from subnet" }), "10.50.0.0/24");

    // NextFreePicker fetches that subnet's grid and suggests its lowest
    // free address (10.50.0.1 is the gateway, so 10.50.0.2 is first free).
    const suggestButton = await screen.findByRole("button", { name: "Suggest 10.50.0.2" });
    await userEvent.click(suggestButton);

    expect(screen.getByDisplayValue("10.50.0.2/24")).toBeInTheDocument();
  });
});
