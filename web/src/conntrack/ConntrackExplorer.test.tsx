// T-1305 acceptance criterion 5: filter application and NAT-column
// rendering against a seeded fixture set. Network is mocked at the
// ./conntrackQueries seam (mirrors flows/FlowExplorer.test.tsx's identical
// pattern) so these tests never touch fetch.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConntrackEntry, ConntrackPage } from "../api/types";
import type { ConntrackFilter } from "../api/conntrack";
import { ConntrackExplorer } from "./ConntrackExplorer";

let queryResult: { data: ConntrackPage | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [] },
  isLoading: false,
  error: null,
};
let lastFilter: ConntrackFilter | undefined;

vi.mock("./conntrackQueries", () => ({
  useConntrackQuery: (filter: ConntrackFilter) => {
    lastFilter = filter;
    return queryResult;
  },
}));

function entry(overrides: Partial<ConntrackEntry> = {}): ConntrackEntry {
  return {
    node: "pve1",
    srcIp: "10.0.0.5",
    dstIp: "10.0.0.10",
    srcPort: 54321,
    dstPort: 443,
    proto: 6,
    state: "ESTABLISHED",
    timeoutSec: 431999,
    ...overrides,
  };
}

function renderExplorer(initialEntries: string[] = ["/conntrack"]): void {
  render(
    <MemoryRouter initialEntries={initialEntries}>
      <ConntrackExplorer />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  queryResult = { data: { items: [] }, isLoading: false, error: null };
  lastFilter = undefined;
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("ConntrackExplorer", () => {
  it("renders the current live table", async () => {
    queryResult = { data: { items: [entry()] }, isLoading: false, error: null };
    renderExplorer();
    await waitFor(() => {
      expect(screen.getByText("pve1")).toBeInTheDocument();
    });
    expect(screen.getByText("10.0.0.5:54321")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.10:443")).toBeInTheDocument();
    expect(screen.getByText("ESTABLISHED")).toBeInTheDocument();
  });

  it("shows an empty state when nothing matches", () => {
    renderExplorer();
    expect(screen.getByText("No live connections match the current filter")).toBeInTheDocument();
  });

  it("renders NAT source/destination columns when present, — otherwise", async () => {
    queryResult = {
      data: {
        items: [
          entry({
            srcIp: "10.0.0.5", dstIp: "8.8.8.8", natSrc: { ip: "203.0.113.10", port: 44444 },
          }),
          entry({ srcIp: "203.0.113.5", dstIp: "203.0.113.10", natDst: { ip: "10.0.0.11", port: 80 } }),
        ],
      },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    await waitFor(() => {
      expect(screen.getByText("203.0.113.10:44444")).toBeInTheDocument();
    });
    expect(screen.getByText("10.0.0.11:80")).toBeInTheDocument();
    // Every row without a NAT translation on that side renders "—", never
    // a blank cell.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  it("shows a partial-results banner naming unreachable nodes", () => {
    queryResult = {
      data: { items: [entry()], partial: true, failedNodes: ["pve3"] },
      isLoading: false,
      error: null,
    };
    renderExplorer();
    expect(screen.getByText(/Could not reach: pve3/)).toBeInTheDocument();
  });

  it("applies filter input changes to the query filter", async () => {
    const user = userEvent.setup();
    renderExplorer();

    await user.type(screen.getByLabelText("Filter by source IP"), "10.0.0.5");
    await waitFor(() => {
      expect(lastFilter?.srcIp).toBe("10.0.0.5");
    });

    await user.type(screen.getByLabelText("Filter by port"), "443");
    await waitFor(() => {
      expect(lastFilter?.port).toBe(443);
    });

    await user.type(screen.getByLabelText("Filter by state"), "ESTABLISHED");
    await waitFor(() => {
      expect(lastFilter?.state).toBe("ESTABLISHED");
    });
  });

  it("pre-fills the filter from URL state (a map/flow-explorer deep link)", () => {
    renderExplorer(["/conntrack?node=pve2&guest=guest%3Apve1%3A104"]);
    expect(screen.getByLabelText("Filter by node")).toHaveValue("pve2");
    expect(screen.getByLabelText("Filter by guest ref")).toHaveValue("guest:pve1:104");
  });
});
