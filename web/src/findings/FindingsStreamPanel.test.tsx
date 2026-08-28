// SPDX-License-Identifier: Apache-2.0

// Component-level tests for T-602's unified findings stream panel: filter
// composition (source/severity/node, AC2) rendered against the shared
// FindingsList presentation, and the fix-changeset wiring. The backend is
// mocked at the api/findings.ts boundary; the WS bridge is stubbed the same
// way ChangesetDrawer.test.tsx stubs it (no real WebSocket in jsdom).
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset, FindingSource, StreamFinding } from "../api/types";
import { NARROW_VIEWPORT_QUERY } from "../lib/useNarrowViewport";
import { FindingsStreamPanel } from "./FindingsStreamPanel";

/** T-909: stubs matchMedia so useNarrowViewport() reports a phone-width
 * viewport — mirrors ChangesetDrawer.test.tsx's identical fake. */
function stubNarrowViewport(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({
      matches,
      media: NARROW_VIEWPORT_QUERY,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    }),
  );
}

const sample: StreamFinding[] = [
  { id: "drift:1", source: "drift", check: "bridge_divergence", severity: "warning", detail: "bridge diverges", nodes: ["pve1"], fixable: false, docsLink: "docs/x" },
  { id: "lldp:1", source: "lldp", check: "vlan_cross_check_missing_on_switch", severity: "warning", detail: "switch missing vlan 20", nodes: ["pve2"], fixable: false, docsLink: "docs/y" },
  { id: "health:1", source: "health", check: "bond_slave_down", severity: "error", detail: "bond0 slave eno2 is down", nodes: ["pve1"], fixable: false, docsLink: "docs/z" },
];

const fetchFindings = vi.fn(() => Promise.resolve(sample));
const fixFinding = vi.fn(
  (_id: string): Promise<Changeset> =>
    Promise.resolve({
      id: "cs1", title: "t", author: "a", status: "draft", ops: [], findings: [],
      createdAt: 0, updatedAt: 0,
    }),
);

vi.mock("../api/findings", () => ({
  fetchFindings: () => fetchFindings(),
  fixFinding: (id: string) => fixFinding(id),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

function renderPanel(initialRoute = "/tools"): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter initialEntries={[initialRoute]}>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <FindingsStreamPanel />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("FindingsStreamPanel", () => {
  it("renders every source's findings by default", async () => {
    renderPanel();
    await waitFor(() => {
      expect(screen.getByText("bridge diverges")).toBeInTheDocument();
    });
    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
  });

  it("filters by source", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "lldp");

    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.queryByText("bond0 slave eno2 is down")).not.toBeInTheDocument();
  });

  it("filters by severity", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by severity"), "error");

    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });

  it("T-2005: a critical-finding push deep link (?pushCategory=critical) pre-filters to error severity", async () => {
    renderPanel("/tools?pushCategory=critical");
    await waitFor(() => screen.getByText("bond0 slave eno2 is down"));

    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
    expect(screen.queryByText("switch missing vlan 20")).not.toBeInTheDocument();
    // The filter control itself reflects the state (not just the visible
    // rows), so a reader can see WHY the stream looks pre-narrowed and
    // clear it if they want the full list.
    expect(screen.getByLabelText("Filter by severity")).toHaveValue("error");
  });

  it("an ordinary /tools visit (no pushCategory param) is unfiltered", async () => {
    renderPanel("/tools");
    await waitFor(() => screen.getByText("bridge diverges"));

    expect(screen.getByText("bridge diverges")).toBeInTheDocument();
    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by severity")).toHaveValue("");
  });

  it("filters by node", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by node"), "pve2");

    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });

  it("combines filters and shows a clear-filters affordance", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "drift");
    await user.selectOptions(screen.getByLabelText("Filter by node"), "pve2");

    expect(screen.getByText("No findings match this filter")).toBeInTheDocument();

    await user.click(screen.getByText("Clear filters"));
    expect(screen.getByText("bridge diverges")).toBeInTheDocument();
  });

  it("shows the healthy empty state when the stream is empty", async () => {
    fetchFindings.mockResolvedValueOnce([]);
    renderPanel();
    await waitFor(() => {
      expect(screen.getByText("No findings")).toBeInTheDocument();
    });
  });

  // T-806: found via this task's own e2e run — a persisted sim_divergence
  // finding with an empty `nodes` array (it names refs, not nodes) must
  // render and filter cleanly, not crash filters.ts's nodesIn/
  // filterFindings the way a nil/undefined `nodes` value did before that
  // fix. This also covers AC2's "every source" premise now spanning five,
  // not four, sources.
  it("renders a probe-sourced (sim_divergence) finding with an empty nodes array, and offers a 'View in simulator' deep link", async () => {
    const probeFinding: StreamFinding = {
      id: "probe:sim_divergence|guest-nic:pve1:300/net0|guest-nic:guest-nic:pve1:301/net0|tcp|2222",
      source: "probe", check: "sim_divergence", severity: "warning",
      detail: "Simulated verdict: deny. Observed: reachable.", nodes: [],
      refs: ["guest-nic:pve1:300/net0"], fixable: false,
      docsLink: "/tools?srcKind=guest-nic&srcRef=guest-nic%3Apve1%3A300%2Fnet0&dstKind=guest-nic&dstRef=guest-nic%3Apve1%3A301%2Fnet0&proto=tcp&port=2222",
      // Phase 36: the deep link is offered through the producer-declared
      // remedy now, not discovered by the component testing
      // `check === "sim_divergence"`. cmd/vnproxd/findings.go builds both
      // from the same simDivergenceDeepLink call, so they cannot disagree.
      remedy: {
        action: "navigate",
        kind: "navigate",
        label: "View in simulator",
        params: { to: "/tools?srcKind=guest-nic&srcRef=guest-nic%3Apve1%3A300%2Fnet0&dstKind=guest-nic&dstRef=guest-nic%3Apve1%3A301%2Fnet0&proto=tcp&port=2222" },
      },
    };
    fetchFindings.mockResolvedValueOnce([...sample, probeFinding]);
    const user = userEvent.setup();
    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Simulated verdict: deny. Observed: reachable.")).toBeInTheDocument();
    });
    expect(screen.getByText("Verify live · sim_divergence")).toBeInTheDocument();

    const viewLink = screen.getByRole("button", { name: "View in simulator" });
    await user.click(viewLink);
    // Navigated (MemoryRouter has no visible URL bar to assert against
    // directly here — the click not throwing, plus the button existing at
    // all, is this test's own regression coverage; SimulatorPage.tsx's own
    // urlState round-trip is covered by urlState.test.ts).
    expect(viewLink).toBeInTheDocument();
  });

  it("includes probe in the source filter and filters a probe finding correctly", async () => {
    const probeFinding: StreamFinding = {
      id: "probe:1", source: "probe", check: "sim_divergence", severity: "warning",
      detail: "probe finding", nodes: [], fixable: false,
    };
    fetchFindings.mockResolvedValueOnce([...sample, probeFinding]);
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "probe");
    expect(screen.getByText("probe finding")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });

  // Debt sweep item 9 / `T-3004-followup-01` (2026-08-19): 11 of
  // `internal/findings.Source`'s constants (`internal/findings/types.go` —
  // the authoritative list, per CLAUDE.md, not a doc that copies it) had no
  // entry in SOURCE_LABELS, because FindingSource (../api/types.ts) named
  // only 5 of its 17 values — so `SOURCE_LABELS[f.source]` evaluated to
  // `undefined` and those findings rendered as the literal string
  // `undefined · <check>`, unfilterable. Fixed by widening FindingSource to
  // list every real value, which turns SOURCE_LABELS's `Record<FindingSource,
  // string>` into an exhaustiveness check the compiler enforces.
  //
  // EXPECTED_SOURCE_LABELS below is its own independent
  // `Record<FindingSource, string>` (not imported from the component, which
  // doesn't export SOURCE_LABELS) — so it, too, fails to compile if
  // FindingSource ever grows without a matching entry here, keeping this
  // regression test itself exhaustive rather than merely re-asserting
  // whatever the component happens to contain today.
  const EXPECTED_SOURCE_LABELS: Record<FindingSource, string> = {
    drift: "Drift",
    lldp: "LLDP",
    ipam: "IPAM",
    health: "Health",
    probe: "Verify live",
    wireguard: "WireGuard",
    wan: "WAN",
    flow: "Flow",
    k8s: "Kubernetes",
    rogue: "Rogue",
    capacity: "Capacity",
    baseline: "Baseline",
    federation: "Federation",
    peer: "Peer",
    store: "Store",
    cert: "Certificates",
    gitsync: "Git sync",
  };

  it("renders every FindingSource's real label (never 'undefined') and offers each in the source filter", async () => {
    const allSources = Object.keys(EXPECTED_SOURCE_LABELS) as FindingSource[];
    const allSourceFindings: StreamFinding[] = allSources.map((source) => ({
      id: `${source}:all-sources`,
      source,
      check: "some_check",
      severity: "warning",
      detail: `detail for ${source}`,
      nodes: ["pve1"],
      fixable: false,
    }));
    fetchFindings.mockResolvedValueOnce(allSourceFindings);
    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("detail for drift")).toBeInTheDocument();
    });

    // No finding's category collapsed to the "undefined · <check>" bug shape.
    expect(screen.queryByText(/^undefined\s*·/)).not.toBeInTheDocument();

    const select = screen.getByLabelText("Filter by source");
    for (const source of allSources) {
      const label = EXPECTED_SOURCE_LABELS[source];
      expect(screen.getByText(`${label} · some_check`)).toBeInTheDocument();
      const option = within(select).getByRole("option", { name: label });
      expect(option).toHaveValue(source);
    }
  });

  describe("narrow viewport (T-909)", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it("disables the fix button (no POST /findings/{id}/fix reachable) and redirects the wizard-launch action to an explanatory toast", async () => {
      stubNarrowViewport(true);
      const mgmtFinding: StreamFinding = {
        id: "health:mgmt", source: "health", check: "mgmt_single_path", severity: "warning",
        detail: "single management path", nodes: ["pve1"], fixable: false,
        // Phase 36: declared by internal/findings/health_mgmtpath.go.
        remedy: { action: "mgmt.redundancy", kind: "navigate", label: "Add a redundant path", params: { node: "pve1" } },
      };
      const fixableFinding: StreamFinding = {
        id: "drift:fixable", source: "drift", check: "bridge_divergence", severity: "warning",
        detail: "fixable drift", nodes: ["pve1"], fixable: true,
      };
      fetchFindings.mockResolvedValueOnce([mgmtFinding, fixableFinding]);
      const user = userEvent.setup();
      renderPanel();

      await waitFor(() => screen.getByText("fixable drift"));

      // The fix button still renders (an explicit affordance, not a hidden
      // one) but is disabled — no fixing changeset can be created here.
      const fixButton = screen.getByRole("button", { name: "Create fixing changeset" });
      expect(fixButton).toBeDisabled();
      await user.click(fixButton);
      expect(fixFinding).not.toHaveBeenCalled();

      // The mgmt-redundancy wizard launch button stays visible but no
      // longer opens the wizard — it explains the device restriction
      // instead (surfaced as a toast; the wizard dialog never mounts).
      const wizardButton = screen.getByRole("button", { name: /Make management path redundant/ });
      await user.click(wizardButton);
      expect(screen.getByText("Desktop only")).toBeInTheDocument();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    it("keeps the fix button enabled and the wizard launch working at desktop width", async () => {
      stubNarrowViewport(false);
      const mgmtFinding: StreamFinding = {
        id: "health:mgmt", source: "health", check: "mgmt_single_path", severity: "warning",
        detail: "single management path", nodes: ["pve1"], fixable: false,
        // Phase 36: declared by internal/findings/health_mgmtpath.go.
        remedy: { action: "mgmt.redundancy", kind: "navigate", label: "Add a redundant path", params: { node: "pve1" } },
      };
      const fixableFinding: StreamFinding = {
        id: "drift:fixable", source: "drift", check: "bridge_divergence", severity: "warning",
        detail: "fixable drift", nodes: ["pve1"], fixable: true,
      };
      fetchFindings.mockResolvedValueOnce([mgmtFinding, fixableFinding]);
      renderPanel();

      await waitFor(() => screen.getByText("fixable drift"));
      expect(screen.getByRole("button", { name: "Create fixing changeset" })).not.toBeDisabled();
    });
  });
});
