// SPDX-License-Identifier: Apache-2.0

// T-3004 AC2: the capacity export is reachable for a link and for an IPAM
// pool, passes both required query parameters, and states the retention
// bound.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CapacityExport, IpamSubnetsResponse, TopologyResponse } from "../api/types";
import { DEFAULT_CAPACITY_RETENTION_DAYS, capacityExportCsvHref, capacityExportQuery } from "../api/capacity";
import { linkEntities, poolEntities } from "./capacityEntities";
import { CapacityExportPanel } from "./CapacityExportPanel";

const emptyExport: CapacityExport = { ref: "", kind: "link", aggregates: [] };
let exportResult: { data: CapacityExport | undefined; isLoading: boolean; error: Error | null } = {
  data: emptyExport,
  isLoading: false,
  error: null,
};

const topology: Pick<TopologyResponse, "nodes"> = {
  nodes: [
    { id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
    {
      id: "phys-group:pve2",
      kind: "phys-group",
      label: "pve2 physical",
      layer: "phys",
      nodeGroup: "pve2",
      status: "ok",
      badges: [],
      members: ["physnic:pve2:eno1", "bridge:pve2:vmbr0"],
    },
  ],
};

const subnets: IpamSubnetsResponse = {
  items: [
    { cidr: "10.0.0.0/24", source: "sdn", vnet: "vnet1", total: 254, allocated: 10, observed: 8, conflicts: 0, utilization: 3.9 },
    { cidr: "10.9.9.0/24", source: "sdn", total: 0, allocated: 0, observed: 0, conflicts: 0, utilization: 0 },
  ],
  generatedAt: 0,
};

vi.mock("../topology/queries", () => ({
  useTopologyQuery: () => ({ data: topology }),
}));
vi.mock("../ipam/queries", () => ({
  useIpamSubnetsQuery: () => ({ data: subnets }),
}));
vi.mock("./analysisQueries", () => ({
  useCapacityExportQuery: () => exportResult,
}));

describe("capacityExportQuery", () => {
  it("always sends both required parameters plus an explicit format", () => {
    const qs = capacityExportQuery("physnic:pve1:eno1", "link", "csv");
    const parsed = new URLSearchParams(qs);
    expect(parsed.get("ref")).toBe("physnic:pve1:eno1");
    expect(parsed.get("kind")).toBe("link");
    expect(parsed.get("format")).toBe("csv");
  });

  it("percent-encodes a Ref's colons so the URL round-trips", () => {
    expect(capacityExportCsvHref("physnic:pve1:eno1", "link")).toBe(
      "/api/v1/capacity/export?ref=physnic%3Apve1%3Aeno1&kind=link&format=csv",
    );
  });
});

describe("capacity entity derivation", () => {
  it("finds physical NICs including those absorbed into a phys-group pill", () => {
    expect(linkEntities(topology.nodes)).toEqual([
      { ref: "physnic:pve1:eno1", label: "pve1 / eno1" },
      { ref: "physnic:pve2:eno1", label: "pve2 / eno1" },
    ]);
  });

  it("offers only pools the rollup actually writes a bucket for", () => {
    // A zero-sized subnet is skipped by capacityBucketSource.poolAggregates,
    // so offering it would produce an export that reads as "no history" when
    // the truth is "never collected".
    expect(poolEntities(subnets.items)).toEqual([{ ref: "10.0.0.0/24", label: "10.0.0.0/24 (vnet1)" }]);
  });
});

describe("CapacityExportPanel", () => {
  it("states the retention bound next to the download", () => {
    exportResult = { data: emptyExport, isLoading: false, error: null };
    render(<CapacityExportPanel />);
    const note = screen.getByTestId("capacity-retention-note");
    expect(note).toHaveTextContent("aggregate_retention_days");
    expect(note).toHaveTextContent(String(DEFAULT_CAPACITY_RETENTION_DAYS));
  });

  it("offers a link export with both query parameters", () => {
    exportResult = { data: emptyExport, isLoading: false, error: null };
    render(<CapacityExportPanel />);
    const href = screen.getByTestId("capacity-export-csv").getAttribute("href") ?? "";
    const parsed = new URLSearchParams(href.split("?")[1] ?? "");
    expect(parsed.get("kind")).toBe("link");
    expect(parsed.get("ref")).toBe("physnic:pve1:eno1");
  });

  it("offers an IPAM pool export, and switching kind never leaves a link ref behind", async () => {
    exportResult = { data: emptyExport, isLoading: false, error: null };
    render(<CapacityExportPanel />);

    await userEvent.selectOptions(screen.getByLabelText("Entity kind"), "ipam_pool");

    const href = screen.getByTestId("capacity-export-csv").getAttribute("href") ?? "";
    const parsed = new URLSearchParams(href.split("?")[1] ?? "");
    expect(parsed.get("kind")).toBe("ipam_pool");
    // A pool ref is a plain CIDR, not a Ref triplet — the two kinds do not
    // share a scheme, so a stale link ref here would be a silently wrong URL.
    expect(parsed.get("ref")).toBe("10.0.0.0/24");
  });

  it("says no history rather than showing an empty table", () => {
    exportResult = { data: { ref: "physnic:pve1:eno1", kind: "link", aggregates: [] }, isLoading: false, error: null };
    render(<CapacityExportPanel />);
    expect(screen.getByText("No history within the retention window")).toBeInTheDocument();
  });
});
