// SPDX-License-Identifier: Apache-2.0

// T-1304 AC5: InteriorTab.test.tsx covers the opt-in toggle copy/gating
// and rendering of the interior data + IPAM diff against a mocked API
// response.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { GuestInterior, GuestInteriorToggle } from "../api/types";
import { InteriorTab } from "./InteriorTab";

const REF = "guest:pve1:200";

let toggleState: GuestInteriorToggle;
let interior: GuestInterior;

const fetchGuestInteriorToggle = vi.fn((_ref: string) => Promise.resolve(toggleState));
const setGuestInteriorToggle = vi.fn((_ref: string, enabled: boolean) => {
  toggleState = { ref: REF, enabled };
  return Promise.resolve(toggleState);
});
const fetchGuestInterior = vi.fn((_ref: string) => Promise.resolve(interior));

vi.mock("../api/guestInterior", () => ({
  fetchGuestInteriorToggle: (ref: string) => fetchGuestInteriorToggle(ref),
  setGuestInteriorToggle: (ref: string, enabled: boolean) => setGuestInteriorToggle(ref, enabled),
  fetchGuestInterior: (ref: string) => fetchGuestInterior(ref),
}));

function renderTab() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <InteriorTab entityRef={REF} />
    </QueryClientProvider>,
  );
}

describe("InteriorTab", () => {
  // `restoreMocks` (vite.config.ts) restores implementations between
  // tests, but these mocks are created once at module scope for the
  // `vi.mock("../api/guestInterior", ...)` factory to close over, so
  // their call history otherwise accumulates across tests in this file —
  // explicit clearing keeps each test's `toHaveBeenCalled*` assertions
  // (and the exact-call-timing this file's newest test depends on)
  // scoped to that test alone.
  beforeEach(() => {
    fetchGuestInteriorToggle.mockClear();
    setGuestInteriorToggle.mockClear();
    fetchGuestInterior.mockClear();
  });

  it("shows the opt-in copy and does not fetch the interior view when the toggle is off", async () => {
    toggleState = { ref: REF, enabled: false };
    renderTab();

    await screen.findByText("Show this guest's network interior");
    await screen.findByText(/Off by default: turning this on reads inside the guest itself/);
    await screen.findByText(/Enable the toggle above to read this guest's interfaces/);

    await waitFor(() => {
      expect(fetchGuestInteriorToggle).toHaveBeenCalled();
    });
    expect(fetchGuestInterior).not.toHaveBeenCalled();

    const checkbox = screen.getByRole("checkbox", { name: /Show this guest's network interior/i });
    expect(checkbox).not.toBeChecked();
  });

  it("fetches and renders the interior view once the toggle is already on", async () => {
    toggleState = { ref: REF, enabled: true };
    interior = {
      interfaces: [{ name: "eth0", mac: "bc:24:11:aa:00:01", mtu: 1500, up: true }],
      addresses: [{ interface: "eth0", ip: "10.10.0.200", family: "ipv4", prefix: 24 }],
      routes: [{ destination: "default", gateway: "10.10.0.1", dev: "eth0" }],
      dns: { nameservers: ["1.1.1.1"], searchDomains: ["example.com"] },
      listeningSockets: [{ proto: "tcp", localAddr: "0.0.0.0", localPort: 22 }],
      defaultGatewayReachable: true,
      source: "qemu-ga",
      ipamDiff: [{ ip: "10.10.0.200", claimed: true, allocated: true, matches: true }],
    };
    renderTab();

    await waitFor(() => {
      expect(fetchGuestInterior).toHaveBeenCalledWith(REF);
    });

    await screen.findByTestId("interior-view");
    expect(screen.getByText("qemu-ga")).toBeInTheDocument();
    expect(screen.getByText("reachable")).toBeInTheDocument();
    expect(screen.getByText("eth0")).toBeInTheDocument();
    expect(screen.getByText(/10\.10\.0\.200\/24/)).toBeInTheDocument();
    expect(screen.getByText("IPAM match")).toBeInTheDocument();
    expect(screen.getByText(/default via 10\.10\.0\.1/)).toBeInTheDocument();
    expect(screen.getByText(/tcp 0\.0\.0\.0:22/)).toBeInTheDocument();

    const checkbox = screen.getByRole("checkbox", { name: /Show this guest's network interior/i });
    expect(checkbox).toBeChecked();
  });

  it("flags an address with no matching IPAM allocation", async () => {
    toggleState = { ref: REF, enabled: true };
    interior = {
      interfaces: [],
      addresses: [{ interface: "eth0", ip: "10.10.0.99", family: "ipv4", prefix: 24 }],
      routes: [],
      dns: {},
      listeningSockets: [],
      defaultGatewayReachable: false,
      source: "lxc-host",
      ipamDiff: [{ ip: "10.10.0.99", claimed: true, allocated: false, matches: false }],
    };
    renderTab();

    await screen.findByTestId("interior-view");
    expect(screen.getByText("no IPAM record")).toBeInTheDocument();
    expect(screen.getByText("not reachable")).toBeInTheDocument();
  });

  it("flipping the toggle on calls the mutation and then fetches the interior view", async () => {
    toggleState = { ref: REF, enabled: false };
    interior = {
      interfaces: [],
      addresses: [],
      routes: [],
      dns: {},
      listeningSockets: [],
      defaultGatewayReachable: false,
      source: "qemu-ga",
      ipamDiff: [],
    };
    const user = userEvent.setup();
    renderTab();

    const checkbox = await screen.findByRole("checkbox", { name: /Show this guest's network interior/i });
    await user.click(checkbox);

    await waitFor(() => {
      expect(setGuestInteriorToggle).toHaveBeenCalledWith(REF, true);
    });
    await waitFor(() => {
      expect(fetchGuestInterior).toHaveBeenCalled();
    });
  });

  // T-2505-followup-02: under CPU restriction, the interior read can race
  // ahead of the guest agent and correctly 404 (`interior_not_enabled`)
  // even though the toggle is genuinely on. That 404 is documented above
  // as an expected, swallowed "no data yet" state — not a fetch error —
  // but a queryFn resolving `undefined` for it, as this hook used to,
  // makes TanStack Query itself throw ("data is undefined"), wedging the
  // query in `isError` and permanently showing the amber "Could not read
  // this guest's interior right now" copy, because the query is now
  // sitting in a genuine (if synthetic) error state a plain refetch
  // doesn't clear on its own. This mounts with the toggle already on (no
  // mutation in play) so the only interior fetch in scope is the one
  // this test controls — nothing else can mask the bug by kicking off a
  // second, coincidentally-successful fetch.
  it("does not surface the raced interior 404 as a fetch error, and still refetches", async () => {
    toggleState = { ref: REF, enabled: true };
    interior = {
      interfaces: [{ name: "eth0", mac: "bc:24:11:aa:00:01", mtu: 1500, up: true }],
      addresses: [],
      routes: [],
      dns: {},
      listeningSockets: [],
      defaultGatewayReachable: true,
      source: "qemu-ga",
      ipamDiff: [],
    };
    // The one and only interior read in this test 404s, as if it landed
    // before the guest agent caught up with an already-on toggle.
    fetchGuestInterior.mockImplementationOnce(() =>
      Promise.reject(new ApiError(404, "interior_not_enabled", "guest interior not enabled")),
    );
    const user = userEvent.setup();
    renderTab();

    await waitFor(() => {
      expect(fetchGuestInterior).toHaveBeenCalled();
    });

    // The query must have actually settled (not still loading) before the
    // "no error" assertion below means anything.
    await waitFor(() => {
      expect(screen.queryByText("Reading guest interior…")).not.toBeInTheDocument();
    });

    expect(screen.queryByText(/Could not read this guest's interior right now/)).not.toBeInTheDocument();

    // And the query must still be capable of refetching afterward — not
    // wedged into a state a real read can never clear.
    await user.click(screen.getByRole("button", { name: "Refresh" }));

    await screen.findByTestId("interior-view");
    expect(screen.getByText("qemu-ga")).toBeInTheDocument();
    expect(screen.queryByText(/Could not read this guest's interior right now/)).not.toBeInTheDocument();
  });
});
