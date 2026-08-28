// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { FIBRoute } from "../api/types";
import { NextHopGraph } from "./NextHopGraph";

function route(overrides: Partial<FIBRoute> = {}): FIBRoute {
  return {
    afi: "ipv4",
    table: "main",
    type: "unicast",
    dst: "192.168.1.0/24",
    dev: "vmbr0",
    ...overrides,
  };
}

describe("NextHopGraph", () => {
  it("renders an empty state when there are no main-table unicast routes", () => {
    render(<NextHopGraph routes={[route({ table: "local", type: "local" })]} />);
    expect(screen.getByText(/no forwarding routes/i)).toBeInTheDocument();
  });

  it("groups routes by device", () => {
    render(
      <NextHopGraph
        routes={[
          route({ dst: "0.0.0.0/0", gateway: "192.168.1.1", dev: "vmbr0" }),
          route({ dst: "192.168.1.0/24", dev: "vmbr0" }),
          route({ dst: "10.99.0.0/24", dev: "vmbr99" }),
        ]}
      />,
    );
    expect(screen.getByText("vmbr0")).toBeInTheDocument();
    expect(screen.getByText("vmbr99")).toBeInTheDocument();
    expect(screen.getByText(/0\.0\.0\.0\/0 via 192\.168\.1\.1/)).toBeInTheDocument();
    expect(screen.getAllByText(/on-link/).length).toBe(2);
  });

  it("excludes local-table and non-unicast routes from the graph", () => {
    render(
      <NextHopGraph
        routes={[
          route({ dst: "192.168.1.0/24", dev: "vmbr0" }),
          route({ table: "local", type: "local", dst: "192.168.1.9/32", dev: "vmbr0" }),
          route({ table: "local", type: "broadcast", dst: "192.168.1.255", dev: "vmbr0" }),
        ]}
      />,
    );
    // Only the one main-table unicast destination should appear.
    expect(screen.getByText(/192\.168\.1\.0\/24 \(on-link\)/)).toBeInTheDocument();
    expect(screen.queryByText(/192\.168\.1\.9/)).not.toBeInTheDocument();
    expect(screen.queryByText(/192\.168\.1\.255/)).not.toBeInTheDocument();
  });

  it("highlights the matched route from a lookup result", () => {
    const highlighted = route({ dst: "0.0.0.0/0", gateway: "192.168.1.1", dev: "vmbr0" });
    render(
      <NextHopGraph
        routes={[highlighted, route({ dst: "10.99.0.0/24", dev: "vmbr99" })]}
        highlighted={highlighted}
      />,
    );
    const highlightedChip = screen.getByText(/0\.0\.0\.0\/0 via 192\.168\.1\.1/);
    expect(highlightedChip.className).toMatch(/border-blue-500/);
    const other = screen.getByText(/10\.99\.0\.0\/24/);
    expect(other.className).not.toMatch(/border-blue-500/);
  });
});
