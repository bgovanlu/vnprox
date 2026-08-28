// SPDX-License-Identifier: Apache-2.0

// The IPv6 segments panel's honesty invariants. All three of these are the
// same house rule from a different angle: an absence of observation is not
// an observation of absence.
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { IPv6SegmentsView, SdnTree } from "../api/types";
import { IPv6SegmentsPanel } from "./IPv6SegmentsPanel";

let segmentsResult: { data: IPv6SegmentsView | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};
const sdnResult: { data: SdnTree | undefined } = {
  data: { zones: [], fabrics: [], controllers: [], ipams: [], prefixLists: [], routeMaps: [], generatedAt: 0 },
};

vi.mock("./ipv6Queries", async () => {
  const actual = await vi.importActual<typeof import("./ipv6Queries")>("./ipv6Queries");
  return { ...actual, useIPv6SegmentsQuery: () => segmentsResult };
});
vi.mock("../sdn/queries", () => ({
  useSdnQuery: () => sdnResult,
}));
vi.mock("./DualStackWizard", () => ({
  DualStackWizard: () => <div data-testid="wizard-stub" />,
}));

describe("IPv6SegmentsPanel", () => {
  it("reads an empty result as 'none observed', not as 'IPv6 is off'", () => {
    segmentsResult = { data: { items: [], generatedAt: 0 }, isLoading: false, error: null };
    render(<IPv6SegmentsPanel />);

    const empty = screen.getByText("No router advertisements observed");
    expect(empty).toBeInTheDocument();
    expect(screen.getByText(/does not mean IPv6 is disabled/)).toBeInTheDocument();
  });

  it("names the nodes that did not answer on a partial read", () => {
    segmentsResult = {
      data: {
        generatedAt: 0,
        partial: true,
        failedNodes: ["pve3"],
        items: [{ node: "pve1", iface: "vmbr0", kind: "bridge", raPresent: true, prefixes: ["2001:db8::/64"] }],
      },
      isLoading: false,
      error: null,
    };
    render(<IPv6SegmentsPanel />);

    expect(screen.getByRole("status")).toHaveTextContent("pve3");
    expect(screen.getByRole("status")).toHaveTextContent("not absent from the cluster");
  });

  it("distinguishes an implied DHCPv6 server from an observed one", () => {
    segmentsResult = {
      data: {
        generatedAt: 0,
        items: [
          {
            node: "pve1",
            iface: "vnet20",
            kind: "vnet",
            vnet: "vnet20",
            zone: "dsz",
            raPresent: true,
            managedFlag: true,
            dhcpv6ServerPresent: true,
            dhcpv6InferredFromRA: true,
          },
        ],
      },
      isLoading: false,
      error: null,
    };
    render(<IPv6SegmentsPanel />);

    expect(screen.getByText(/DHCPv6 server implied by the RA, not observed/)).toBeInTheDocument();
  });

  it("says a failed read failed, rather than showing an empty segment list", () => {
    segmentsResult = { data: undefined, isLoading: false, error: new Error("fan-out broke") };
    render(<IPv6SegmentsPanel />);

    expect(screen.getByText("Could not read IPv6 segments")).toBeInTheDocument();
    expect(screen.queryByText("No router advertisements observed")).not.toBeInTheDocument();
  });
});
