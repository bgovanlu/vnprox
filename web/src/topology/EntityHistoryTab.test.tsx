import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EntityHistoryTab } from "./EntityHistoryTab";
import type { EntityHistoryPage } from "../api/types";

const fetchEntityHistory = vi.hoisted(() => vi.fn());
vi.mock("../api/entityHistory", () => ({ fetchEntityHistory }));

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const page: EntityHistoryPage = {
  items: [
    { kind: "changeset", at: 1_700_000_300, actor: "alice", summary: "bridge.update in widen vmbr0", changesetId: "cs1", result: "committed" },
    { kind: "audit", at: 1_700_000_200, actor: "brian", summary: "changeset.apply", result: "ok" },
    { kind: "snapshot", at: 1_700_000_100, summary: "pre snapshot of pve1", snapshotId: "snap1" },
  ],
  truncated: false,
};

beforeEach(() => {
  fetchEntityHistory.mockReset();
});
afterEach(() => {
  vi.clearAllMocks();
});

describe("EntityHistoryTab", () => {
  it("renders all three merged sources, each labelled", async () => {
    fetchEntityHistory.mockResolvedValue(page);
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr0" />, { wrapper });

    expect(await screen.findByText("bridge.update in widen vmbr0")).toBeInTheDocument();
    expect(screen.getByText("changeset.apply")).toBeInTheDocument();
    expect(screen.getByText("pre snapshot of pve1")).toBeInTheDocument();
    // The kind labels are what keep a merged timeline legible.
    expect(screen.getByText("Changeset")).toBeInTheDocument();
    expect(screen.getByText("Audit")).toBeInTheDocument();
    expect(screen.getByText("Snapshot")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
  });

  it("passes the entity ref to the API verbatim", async () => {
    fetchEntityHistory.mockResolvedValue(page);
    render(<EntityHistoryTab entityRef="sdn-vnet::zone1/vnet1" />, { wrapper });
    await screen.findByText("changeset.apply");
    // A ref containing "/" must reach the API unmangled — see the API module's
    // own note on why it travels in a query parameter.
    expect(fetchEntityHistory).toHaveBeenCalledWith("sdn-vnet::zone1/vnet1");
  });

  // The load-bearing distinction: an incomplete history says so, because
  // "nothing has ever touched this bridge" is a conclusion an operator acts on.
  it("warns when the history is truncated", async () => {
    fetchEntityHistory.mockResolvedValue({ ...page, truncated: true });
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr0" />, { wrapper });
    expect(await screen.findByText(/This history is incomplete/)).toBeInTheDocument();
  });

  it("does not show the truncation warning when the history is complete", async () => {
    fetchEntityHistory.mockResolvedValue(page);
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr0" />, { wrapper });
    await screen.findByText("changeset.apply");
    expect(screen.queryByText(/This history is incomplete/)).not.toBeInTheDocument();
  });

  it("shows an empty state for an entity with no recorded history", async () => {
    fetchEntityHistory.mockResolvedValue({ items: [], truncated: false });
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr9" />, { wrapper });
    expect(await screen.findByText("No recorded history")).toBeInTheDocument();
  });

  // A failure must never render as "nothing has touched this".
  it("distinguishes a load failure from an empty history", async () => {
    fetchEntityHistory.mockRejectedValue(new Error("boom"));
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr0" />, { wrapper });
    expect(await screen.findByText(/Could not load/)).toBeInTheDocument();
    expect(screen.queryByText("No recorded history")).not.toBeInTheDocument();
  });

  it("does not fetch while disabled", () => {
    render(<EntityHistoryTab entityRef="bridge:pve1:vmbr0" enabled={false} />, { wrapper });
    expect(fetchEntityHistory).not.toHaveBeenCalled();
  });
});
