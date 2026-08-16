// T-1404 acceptance criterion 6: the dual-stack rollout wizard produces
// one changeset via blueprint.Instantiate, and re-running it against the
// now-converged state (the backend's own idempotent-instantiate contract,
// mocked here at the mutation boundary) yields zero ops the second time —
// the wizard must render that as "already up to date", not an error or a
// phantom second draft.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Blueprint, Changeset, IPv6SegmentsView } from "../api/types";
import { DUALSTACK_BLUEPRINT_ID, DualStackWizard } from "./DualStackWizard";

const saveMock = vi.fn<(bp: Blueprint) => Promise<Blueprint>>();
const instantiateMock = vi.fn<(args: { id: string; req: { params: Record<string, unknown> } }) => Promise<Changeset>>();

// T-3004: the wizard now reads GET /ipv6/segments. Mocked at the query-hook
// seam so these tests never touch fetch, matching how every other panel test
// in this repo isolates its network reads.
let segmentsResult: { data: IPv6SegmentsView | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};

vi.mock("./ipv6Queries", async () => {
  const actual = await vi.importActual<typeof import("./ipv6Queries")>("./ipv6Queries");
  return { ...actual, useIPv6SegmentsQuery: () => segmentsResult };
});

vi.mock("../blueprints/queries", async () => {
  const actual = await vi.importActual<typeof import("../blueprints/queries")>("../blueprints/queries");
  return {
    ...actual,
    useBlueprintsQuery: () => ({ data: { items: [] }, isLoading: false, error: null }),
    useSaveBlueprintMutation: () => ({ mutateAsync: saveMock }),
    useInstantiateBlueprintMutation: () => ({ mutateAsync: instantiateMock }),
  };
});

function renderWizard() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <DualStackWizard vnets={[{ id: "vnet20", alias: "healthy-dualstack", zone: "dsz" }]} />
    </QueryClientProvider>,
  );
}

function savedBlueprint(): Blueprint {
  return {
    blueprintVersion: 1,
    id: DUALSTACK_BLUEPRINT_ID,
    name: "Dual-stack IPv6 rollout",
    nodeSelector: { mode: "all" },
    params: [],
    entities: [],
  };
}

describe("DualStackWizard — T-1404 AC6", () => {
  afterEach(() => {
    saveMock.mockReset();
    instantiateMock.mockReset();
  });

  it("first run stages one op; re-running against converged state yields zero ops", async () => {
    const user = userEvent.setup();
    saveMock.mockResolvedValue(savedBlueprint());
    instantiateMock
      .mockResolvedValueOnce({
        id: "cs-1", title: "dual-stack IPv6: vnet20", author: "root", status: "draft",
        ops: [{ op: "sdn.subnet.create", target: "sdn-subnet::2001:db8:20::/64", params: {} }],
        findings: [], createdAt: 1, updatedAt: 1,
      })
      .mockResolvedValueOnce({
        id: "cs-2", title: "dual-stack IPv6: vnet20", author: "root", status: "draft",
        ops: [], findings: [], createdAt: 2, updatedAt: 2,
      });

    renderWizard();

    const cidrField = screen.getByLabelText(/IPv6 subnet/);
    await user.type(cidrField, "2001:db8:20::/64");
    await user.click(screen.getByRole("button", { name: "Roll out IPv6" }));

    await waitFor(() => { expect(instantiateMock).toHaveBeenCalledTimes(1); });
    expect(instantiateMock.mock.calls[0]?.[0]).toMatchObject({
      id: DUALSTACK_BLUEPRINT_ID,
      req: { params: { vnet: "vnet20", cidr: "2001:db8:20::/64", snat: true } },
    });
    // First run: blueprint didn't exist yet — saved exactly once.
    expect(saveMock).toHaveBeenCalledTimes(1);
    await screen.findByText(/Draft changeset cs-1 created with 1 op\(s\)/);

    // Re-run the identical request (the "now-converged state" case). The
    // wizard must not re-save the blueprint (it already exists) and must
    // report the zero-op result plainly, not as an error.
    await user.click(screen.getByRole("button", { name: "Roll out IPv6" }));

    await waitFor(() => { expect(instantiateMock).toHaveBeenCalledTimes(2); });
    expect(saveMock).toHaveBeenCalledTimes(1); // still just once
    await screen.findByText(/vnet20 is already up to date — no changes needed\./);
  });
});

// T-3004 AC4: the wizard reads the state it edits. The component was
// previously mounted nowhere and called no route at all; these tests pin the
// three distinct readings of GET /ipv6/segments so none of them can quietly
// collapse into another.
describe("DualStackWizard — reads GET /ipv6/segments", () => {
  afterEach(() => {
    saveMock.mockReset();
    instantiateMock.mockReset();
    segmentsResult = { data: { items: [], generatedAt: 0 }, isLoading: false, error: null };
  });

  it("shows the IPv6 already live on the selected VNet", () => {
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
            prefixes: ["2001:db8:20::/64"],
          },
          // A different VNet's segment must not leak into this readout.
          { node: "pve1", iface: "vnet99", kind: "vnet", vnet: "vnet99", raPresent: true, prefixes: ["2001:db8:99::/64"] },
        ],
      },
      isLoading: false,
      error: null,
    };
    renderWizard();

    const observed = screen.getByTestId("dualstack-observed");
    expect(observed).toHaveTextContent("2001:db8:20::/64");
    expect(observed).not.toHaveTextContent("2001:db8:99::/64");
  });

  it("reads 'no RA observed' as the ordinary starting point, not a fault", () => {
    renderWizard();
    const observed = screen.getByTestId("dualstack-observed");
    expect(observed).toHaveTextContent("No router advertisement observed");
    expect(observed).toHaveTextContent("ordinary starting point");
  });

  it("distinguishes a failed read from an absence of IPv6", () => {
    segmentsResult = { data: undefined, isLoading: false, error: new Error("peer unreachable") };
    renderWizard();

    const observed = screen.getByTestId("dualstack-observed");
    expect(observed).toHaveTextContent("failed read, not an absence of IPv6");
    expect(observed).not.toHaveTextContent("No router advertisement observed");
  });

  it("names the nodes that did not answer on a partial read", () => {
    segmentsResult = {
      data: { generatedAt: 0, items: [], partial: true, failedNodes: ["pve3"] },
      isLoading: false,
      error: null,
    };
    renderWizard();

    expect(screen.getByTestId("dualstack-observed")).toHaveTextContent("pve3");
  });
});
