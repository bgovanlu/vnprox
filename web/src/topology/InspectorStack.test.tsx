// T-908 acceptance criterion 1: pinning an inspector and selecting a
// second entity shows two open inspectors simultaneously; unpinning/
// closing one leaves the other intact. Uses two distinct bridge fixtures
// (different node) — kind matching doesn't matter for this pin-state test,
// only pane count/identity does; InspectorCompareView.test.tsx covers the
// compare-layout-specific rendering.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { InspectorStack } from "./InspectorStack";

function bridgeDetail(ref: string, node: string, label: string): EntityDetail {
  return { ref, kind: "bridge", node, label, fields: {}, provenance: {}, related: [], generatedAt: 1 };
}

const pve1Bridge = bridgeDetail("bridge:pve1:vmbr0", "pve1", "vmbr0");
const pve2Bridge = bridgeDetail("bridge:pve2:vmbr0", "pve2", "vmbr0-pve2");

const detailByRef: Record<string, EntityDetail> = {
  [pve1Bridge.ref]: pve1Bridge,
  [pve2Bridge.ref]: pve2Bridge,
};

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn((ref: string) => Promise.resolve(detailByRef[ref])),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/metrics", () => ({
  fetchMetricsLive: vi.fn(() => Promise.resolve([])),
  fetchMetricsHistory: vi.fn(() => Promise.resolve([])),
}));

function renderStack(selectedRef: string | undefined, onAllClosed: () => void = () => undefined) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <InspectorStack selectedRef={selectedRef} onAllClosed={onAllClosed} />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, queryClient };
}

describe("InspectorStack pin state", () => {
  it("shows nothing when no ref is selected", () => {
    renderStack(undefined);
    expect(screen.queryByText("vmbr0")).not.toBeInTheDocument();
  });

  it("selecting a second entity without pinning replaces the single inspector (pre-T-908 behavior)", async () => {
    const { rerender, queryClient } = renderStack(pve1Bridge.ref);
    await screen.findByText("vmbr0");

    rerender(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <ToastProvider>
            <InspectorStack selectedRef={pve2Bridge.ref} onAllClosed={() => undefined} />
          </ToastProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText("vmbr0-pve2");
    // Only one inspector is open — the region container (2+ panes) never appears.
    expect(screen.queryByRole("region", { name: /Inspector panes/ })).not.toBeInTheDocument();
  });

  it("pinning the first inspector then selecting a second entity opens both simultaneously", async () => {
    const { rerender, queryClient } = renderStack(pve1Bridge.ref);
    await screen.findByText("vmbr0");

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Pin" }));
    expect(screen.getByRole("button", { name: "Pinned" })).toBeInTheDocument();

    rerender(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <ToastProvider>
            <InspectorStack selectedRef={pve2Bridge.ref} onAllClosed={() => undefined} />
          </ToastProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText("vmbr0")).toBeInTheDocument();
    expect(await screen.findByText("vmbr0-pve2")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: /Inspector panes \(2\)/ })).toBeInTheDocument();
  });

  it("closing one of two open panes leaves the other intact", async () => {
    const { rerender, queryClient } = renderStack(pve1Bridge.ref);
    await screen.findByText("vmbr0");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Pin" }));

    rerender(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <ToastProvider>
            <InspectorStack selectedRef={pve2Bridge.ref} onAllClosed={() => undefined} />
          </ToastProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText("vmbr0-pve2");

    await user.click(screen.getByRole("button", { name: /Close vmbr0-pve2/ }));

    expect(screen.queryByText("vmbr0-pve2")).not.toBeInTheDocument();
    expect(screen.getByText("vmbr0")).toBeInTheDocument();
  });

  it("closing the last remaining unpinned pane (Escape, the modal drawer's existing mechanism) calls onAllClosed", async () => {
    const onAllClosed = vi.fn();
    renderStack(pve1Bridge.ref, onAllClosed);
    await screen.findByText("vmbr0");
    const user = userEvent.setup();
    await user.keyboard("{Escape}");
    expect(onAllClosed).toHaveBeenCalledTimes(1);
  });

  it("pinning a single pane switches it to the non-modal region (so it no longer blocks background clicks) and its own Close button calls onAllClosed", async () => {
    const onAllClosed = vi.fn();
    renderStack(pve1Bridge.ref, onAllClosed);
    await screen.findByText("vmbr0");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Pin" }));

    expect(screen.getByRole("region", { name: /Inspector panes \(1\)/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Close vmbr0\b/ }));
    expect(onAllClosed).toHaveBeenCalledTimes(1);
  });
});
