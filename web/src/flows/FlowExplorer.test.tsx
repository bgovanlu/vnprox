// T-1003 AC1: filter application, sort, and conversation aggregation
// against a seeded flow fixture set — network and WS are mocked at the
// ./flowsQueries seam (mirrors fwlog/FwLogViewer.test.tsx's identical
// pattern) so these tests never touch fetch or a real socket.
import { act } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FlowBatchEvent, FlowRecord, FlowsPage } from "../api/types";
import { FlowExplorer } from "./FlowExplorer";

let queryResult: { data: FlowsPage | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [] },
  isLoading: false,
  error: null,
};
let onBatchHandler: ((evt: FlowBatchEvent) => void) | undefined;

vi.mock("./flowsQueries", () => ({
  useFlowsQuery: () => queryResult,
  useFlowsWsBridge: (onBatch: (evt: FlowBatchEvent) => void) => {
    onBatchHandler = onBatch;
  },
}));

function rec(overrides: Partial<FlowRecord> = {}): FlowRecord {
  return {
    at: 1000,
    node: "pve1",
    srcIp: "10.0.0.5",
    dstIp: "10.0.0.10",
    proto: 6,
    bytes: 1000,
    packets: 10,
    source: "netflow5",
    ...overrides,
  };
}

function pushBatch(entries: FlowRecord[], droppedTotal = 0): void {
  act(() => {
    onBatchHandler?.({ event: "flow.batch", entries, droppedTotal });
  });
}

function renderExplorer(initialEntries: string[] = ["/flows"]): void {
  render(
    <MemoryRouter initialEntries={initialEntries}>
      <FlowExplorer />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  queryResult = { data: { items: [] }, isLoading: false, error: null };
  onBatchHandler = undefined;
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("FlowExplorer", () => {
  it("renders the initial REST page", async () => {
    queryResult = {
      data: { items: [rec({ node: "pve1", srcIp: "10.0.0.5", dstIp: "10.0.0.10" })] },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    await waitFor(() => {
      expect(screen.getByText("pve1")).toBeInTheDocument();
    });
  });

  it("shows the empty-state hint when there are no flow records cluster-wide", () => {
    renderExplorer();
    expect(screen.getByText("No flow records yet")).toBeInTheDocument();
  });

  it("appends a live WS batch and clears the empty-state hint in the same session", async () => {
    renderExplorer();
    expect(screen.getByText("No flow records yet")).toBeInTheDocument();

    pushBatch([rec({ node: "pve2", srcIp: "10.1.1.5", dstIp: "10.1.1.50" })]);

    await waitFor(() => {
      expect(screen.getByText("pve2")).toBeInTheDocument();
    });
    expect(screen.queryByText("No flow records yet")).not.toBeInTheDocument();
  });

  it("filters the rendered rows by protocol", async () => {
    const user = userEvent.setup();
    queryResult = {
      data: {
        items: [
          rec({ node: "pve1", proto: 6, srcIp: "10.0.0.5", dstIp: "10.0.0.10" }),
          rec({ node: "pve2", proto: 17, srcIp: "10.1.1.5", dstIp: "10.1.1.50" }),
        ],
      },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    await waitFor(() => screen.getByText("pve2"));

    await user.type(screen.getByLabelText("Filter by protocol"), "udp");

    await waitFor(() => {
      expect(screen.queryByText("pve1")).not.toBeInTheDocument();
    });
    expect(screen.getByText("pve2")).toBeInTheDocument();
  });

  it("sorts rows by bytes when the Bytes sort is selected", async () => {
    const user = userEvent.setup();
    queryResult = {
      data: {
        items: [
          rec({ node: "pve-small", bytes: 100, at: 1 }),
          rec({ node: "pve-big", bytes: 900, at: 2 }),
        ],
      },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    await waitFor(() => screen.getByText("pve-big"));

    await user.selectOptions(screen.getByLabelText("Sort by"), "bytes");

    const rows = screen.getAllByRole("row").slice(1); // drop the header row
    expect(rows[0]).toHaveTextContent("pve-big");
    expect(rows[1]).toHaveTextContent("pve-small");
  });

  it("aggregates by conversation pair when the Conversations tab is selected", async () => {
    const user = userEvent.setup();
    queryResult = {
      data: {
        items: [
          rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", bytes: 100, packets: 1 }),
          rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", bytes: 200, packets: 2 }),
          rec({ srcRef: "bridge:pve2:vmbr0", dstRef: "sdn-vnet::z/v100", bytes: 50, packets: 1 }),
        ],
      },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    await waitFor(() => screen.getByRole("tab", { name: "Conversations" }));

    await user.click(screen.getByRole("tab", { name: "Conversations" }));

    await waitFor(() => {
      expect(screen.getByText(/bridge:pve1:vmbr0/)).toBeInTheDocument();
    });
    // The two pve1->vnet100 records collapse into one aggregated row with
    // summed bytes/packets — 300 B total for a 300-byte sum.
    expect(screen.getByText("300 B")).toBeInTheDocument();
    // Distinct src (pve2) still gets its own row.
    expect(screen.getByText(/bridge:pve2:vmbr0/)).toBeInTheDocument();
  });

  it("pre-filters to a guest-pair deep link (pairSrc/pairDst URL state)", async () => {
    queryResult = {
      data: {
        items: [
          rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", node: "pve1" }),
          rec({ srcRef: "bridge:pve2:vmbr0", dstRef: "sdn-vnet::z/v100", node: "pve2" }),
        ],
      },
      isLoading: false,
      error: null,
    };
    renderExplorer(["/flows?guest=bridge%3Apve1%3Avmbr0&pairSrc=bridge%3Apve1%3Avmbr0&pairDst=sdn-vnet%3A%3Az%2Fv100"]);

    await waitFor(() => {
      expect(screen.getByText("pve1")).toBeInTheDocument();
    });
    expect(screen.queryByText("pve2")).not.toBeInTheDocument();
  });
});
