// The §5 staleness banner (docs/features/topology.md §5: "its band renders
// greyed from last-known data with a staleness banner and timestamp").
// Driven by the captured stale fixture (real vnproxd staleness object —
// see staleness.test.ts's header comment on how it was captured).
import staleFixture from "./__fixtures__/three-node-vlan-topology-stale.json";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Staleness, TopologyResponse } from "../api/types";
import { StalenessBanner } from "./StalenessBanner";

const stale = staleFixture as unknown as TopologyResponse;

describe("StalenessBanner", () => {
  it("renders nothing when there is no staleness section (healthy topology)", () => {
    const { container } = render(<StalenessBanner staleness={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when all sources are fresh", () => {
    const healthy: Staleness = {
      stale: false,
      sources: [{ name: "pve", stale: false, lastSuccess: 1720512345 }],
    };
    const { container } = render(<StalenessBanner staleness={healthy} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the banner from the captured stale fixture: last-known-data wording, timestamp, and failing source", () => {
    render(<StalenessBanner staleness={stale.staleness} />);
    const banner = screen.getByRole("status");
    // Cluster-wide (pve) staleness → the banner covers the whole map.
    expect(banner).toHaveTextContent("This map is showing last-known data");
    expect(banner).toHaveTextContent("pve");
    expect(banner).toHaveTextContent("whole cluster");
    // §5's "timestamp": the failing source's last successful poll time.
    const pve = stale.staleness?.sources.find((s) => s.name === "pve");
    expect(pve?.lastSuccess).toBeDefined();
    expect(banner).toHaveTextContent(new Date((pve?.lastSuccess ?? 0) * 1000).toLocaleString());
    // The failing source's live error is surfaced too.
    expect(banner).toHaveTextContent("connection refused");
  });

  it("uses the greyed-bands wording when only node-scoped sources are stale", () => {
    const nodeScoped: Staleness = {
      stale: true,
      sources: [{ name: "host", node: "pve2", stale: true, lastSuccess: 1720511745, lastError: "peer unreachable" }],
    };
    render(<StalenessBanner staleness={nodeScoped} />);
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent("Parts of this map are showing last-known data (greyed bands)");
    expect(banner).toHaveTextContent("node pve2");
    expect(banner).toHaveTextContent("peer unreachable");
  });

  it("says 'no successful poll yet' when a stale source never succeeded (lastSuccess absent)", () => {
    const neverPolled: Staleness = {
      stale: true,
      sources: [{ name: "lldp", node: "pve1", stale: true, lastError: "lldpctl not found" }],
    };
    render(<StalenessBanner staleness={neverPolled} />);
    expect(screen.getByRole("status")).toHaveTextContent("no successful poll yet");
  });
});

// Same 2026-08-20 regression as UnrefFindingsBanner's — see that file's
// comment for the measurement. This list grows with the cluster (one entry
// per stale source per node) and sits directly above the map container.
describe("StalenessBanner — bounded height (2026-08-20 regression)", () => {
  it("caps and scrolls its list instead of growing without limit", () => {
    const staleness: Staleness = {
      stale: true,
      sources: Array.from({ length: 40 }, (_, i) => ({
        name: "host",
        node: `pve${String(i)}`,
        stale: true,
        lastSuccess: 1720512345,
        lastError: "dial tcp: connection refused",
      })),
    };
    const { container } = render(<StalenessBanner staleness={staleness} />);
    const list = container.querySelector("ul");
    expect(list).not.toBeNull();
    expect(list?.className).toContain("max-h-");
    expect(list?.className).toContain("overflow-y-auto");
    expect(container.querySelectorAll("li")).toHaveLength(40);
  });
});
