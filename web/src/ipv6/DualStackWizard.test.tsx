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
import type { Blueprint, Changeset } from "../api/types";
import { DUALSTACK_BLUEPRINT_ID, DualStackWizard } from "./DualStackWizard";

const saveMock = vi.fn<(bp: Blueprint) => Promise<Blueprint>>();
const instantiateMock = vi.fn<(args: { id: string; req: { params: Record<string, unknown> } }) => Promise<Changeset>>();

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
