// SPDX-License-Identifier: Apache-2.0

// The §5 staleness banner (docs/features/topology.md §5: "its band renders
// greyed from last-known data with a staleness banner and timestamp").
// Driven by the captured stale fixture (real vnproxd staleness object —
// see staleness.test.ts's header comment on how it was captured).
import staleFixture from "./__fixtures__/three-node-vlan-topology-stale.json";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
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

// T-3603: the banner offers to do something about the staleness it reports.
describe("StalenessBanner — retry (T-3603)", () => {
  const stale: Staleness = {
    stale: true,
    sources: [{ name: "host", node: "pve001", stale: true, lastError: "host links (pve001): context canceled" }],
  };

  function retryProps(over: Partial<NonNullable<Parameters<typeof StalenessBanner>[0]["retry"]>> = {}) {
    return { canRetry: true, pending: false, onRetry: vi.fn(), ...over };
  }

  it("renders no retry affordance at all when none is supplied", () => {
    // The pre-Phase-36 rendering, still reachable: a surface that has no
    // way to run a refresh must not imply one exists.
    render(<StalenessBanner staleness={stale} />);
    expect(screen.queryByRole("button", { name: "Retry now" })).toBeNull();
  });

  it("offers a retry to a session with netWrite", async () => {
    const user = userEvent.setup();
    const retry = retryProps();
    render(<StalenessBanner staleness={stale} retry={retry} />);
    await user.click(screen.getByRole("button", { name: "Retry now" }));
    // No confirmation dialog: this is the read-only operational tier. A
    // dialog asking "re-read the cluster?" would train operators to click
    // through the ones that matter.
    expect(retry.onRetry).toHaveBeenCalledTimes(1);
  });

  it("offers NO retry without netWrite", () => {
    render(<StalenessBanner staleness={stale} retry={retryProps({ canRetry: false })} />);
    expect(screen.queryByRole("button", { name: "Retry now" })).toBeNull();
  });

  it("offers a retry even when a source has never polled successfully", () => {
    // The phase card assumed retry was pointless here. It is not: "no
    // successful poll yet" means "not since this daemon started", which
    // includes "the peer was unreachable until a moment ago" — exactly when
    // an operator reaches for this button.
    const neverPolled: Staleness = {
      stale: true,
      sources: [{ name: "host", node: "pve001", stale: true, lastError: "context canceled" }],
    };
    render(<StalenessBanner staleness={neverPolled} retry={retryProps()} />);
    expect(screen.getByText("no successful poll yet", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry now" })).toBeInTheDocument();
  });

  it("reports a repeated failure rather than going quiet", () => {
    // The same error twice is informative — it says the problem is not
    // transient, which is precisely what this banner could never say
    // before. A retry that silently fails again is worse than no button.
    render(
      <StalenessBanner
        staleness={stale}
        retry={retryProps({ result: { error: "host links (pve001): context canceled", changed: false } })}
      />,
    );
    expect(screen.getByText(/Retry failed — host links \(pve001\): context canceled/)).toBeInTheDocument();
  });

  it("reports success, and whether the map moved", () => {
    render(<StalenessBanner staleness={stale} retry={retryProps({ result: { changed: true } })} />);
    expect(screen.getByText("Retry succeeded — the map has been updated.")).toBeInTheDocument();
  });

  it("surfaces the server's rate limit as a real outcome", () => {
    // Enforced server-side, so the UI has to be able to say "too soon"
    // rather than looking like the button did nothing.
    render(<StalenessBanner staleness={stale} retry={retryProps({ rateLimited: true })} />);
    expect(screen.getByText(/wait a few seconds/)).toBeInTheDocument();
  });

  it("disables the button while a retry is in flight", () => {
    render(<StalenessBanner staleness={stale} retry={retryProps({ pending: true })} />);
    expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
  });
});
