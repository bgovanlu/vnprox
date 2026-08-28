// SPDX-License-Identifier: Apache-2.0

// T-505 firewall log viewer: filter composition, pause/resume, and the
// drop indicator, driven through the real component + reducer (network
// and WS are mocked at the ./queries seam so these tests never touch
// fetch or a real socket).
import { act } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FwLogBatchEvent, FwLogEntry, FwLogPage } from "../api/types";
import { FwLogViewer } from "./FwLogViewer";

let queryResult: { data: FwLogPage | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [], droppedTotal: 0 },
  isLoading: false,
  error: null,
};
let onBatchHandler: ((evt: FwLogBatchEvent) => void) | undefined;

vi.mock("./queries", () => ({
  useFwLogQuery: () => queryResult,
  useFwLogWsBridge: (onBatch: (evt: FwLogBatchEvent) => void) => {
    onBatchHandler = onBatch;
  },
}));

function entry(overrides: Partial<FwLogEntry> = {}): FwLogEntry {
  return {
    seq: 1,
    node: "pve1",
    vmid: 100,
    direction: "in",
    action: "DROP",
    source: "1.1.1.1",
    dest: "2.2.2.2",
    raw: "raw",
    correlation: { status: "unmatched", reason: "test" },
    ...overrides,
  };
}

function pushBatch(entries: FwLogEntry[], droppedTotal = 0): void {
  act(() => {
    onBatchHandler?.({ event: "firewall.log.batch", entries, droppedTotal });
  });
}

function renderViewer(): void {
  render(
    <MemoryRouter>
      <FwLogViewer />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  queryResult = { data: { items: [], droppedTotal: 0 }, isLoading: false, error: null };
  onBatchHandler = undefined;
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("FwLogViewer", () => {
  it("renders the initial REST page", async () => {
    queryResult = {
      data: { items: [entry({ seq: 1, node: "pve1", vmid: 100 })], droppedTotal: 0 },
      isLoading: false,
      error: null,
    };
    renderViewer();
    await waitFor(() => {
      expect(screen.getByText("pve1")).toBeInTheDocument();
    });
    expect(screen.getByText("100")).toBeInTheDocument();
  });

  it("appends a live WS batch to the view", async () => {
    renderViewer();
    expect(screen.getByText("No log lines yet")).toBeInTheDocument();

    pushBatch([entry({ seq: 1, node: "pve2", vmid: 200 })]);

    await waitFor(() => {
      expect(screen.getByText("pve2")).toBeInTheDocument();
    });
  });

  it("filters the rendered rows by node", async () => {
    const user = userEvent.setup();
    queryResult = {
      data: {
        items: [entry({ seq: 1, node: "pve1", vmid: 100 }), entry({ seq: 2, node: "pve2", vmid: 200 })],
        droppedTotal: 0,
      },
      isLoading: false,
      error: null,
    };
    renderViewer();
    await waitFor(() => screen.getByText("pve2"));

    await user.type(screen.getByLabelText("Filter by node"), "pve1");

    await waitFor(() => {
      expect(screen.queryByText("pve2")).not.toBeInTheDocument();
    });
    expect(screen.getByText("pve1")).toBeInTheDocument();
  });

  it("pauses the stream: new WS batches are counted but not rendered until resume", async () => {
    const user = userEvent.setup();
    renderViewer();

    await user.click(screen.getByRole("button", { name: "Pause" }));
    pushBatch([entry({ seq: 1, node: "pve3", vmid: 300 })]);

    expect(screen.queryByText("pve3")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Resume \(1 new\)/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Resume/ }));
    await waitFor(() => {
      expect(screen.getByText("pve3")).toBeInTheDocument();
    });
  });

  it("shows the drop indicator once a server or client drop is reported", async () => {
    renderViewer();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();

    pushBatch([entry({ seq: 1 })], 42);

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("42");
    });
  });

  it("renders a deep-link for a correlated (status: rule) line and a plain badge otherwise", async () => {
    queryResult = {
      data: {
        items: [
          entry({
            seq: 1,
            node: "pve1",
            correlation: { status: "rule", rule: { guestRef: "guest:pve1:100", origin: "guest", pos: 2 } },
          }),
          entry({ seq: 2, node: "pve1", vmid: 101, correlation: { status: "ambiguous", reason: "2 candidates" } }),
        ],
        droppedTotal: 0,
      },
      isLoading: false,
      error: null,
    };
    renderViewer();

    await waitFor(() => screen.getByText("Rule"));
    const link = screen.getByRole("link", { name: /Rule/ });
    expect(link.getAttribute("href") ?? "").toContain("ref=guest%3Apve1%3A100");

    expect(screen.getByText("Ambiguous")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Ambiguous/ })).not.toBeInTheDocument();
  });
});
