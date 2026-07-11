import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EvpnZoneWizard } from "./EvpnZoneWizard";
import { renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

const NAME_FIELD = /^Name/;

async function fillControllerStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "dc-evpn");
  await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
  await user.click(screen.getByRole("checkbox", { name: "pve2" }));
  await user.click(screen.getByRole("checkbox", { name: "pve3" }));
  await user.type(screen.getByRole("textbox", { name: /^Controller ID/ }), "evpn1");
  fireEvent.change(screen.getByRole("spinbutton", { name: /^ASN/ }), { target: { value: "65000" } });
  await user.type(
    screen.getByRole("textbox", { name: /^Peer addresses/ }),
    "10.10.0.11, 10.10.0.12, 10.10.0.13",
  );
  await user.click(screen.getByRole("button", { name: "Next" }));
}

describe("EvpnZoneWizard — T-403 AC1 (golden ops)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("completed against pvemock-shaped ops: produces the exact golden changeset", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<EvpnZoneWizard open onOpenChange={() => undefined} />);

    await fillControllerStep(user);

    // Step 2: exit nodes — pick pve1 as the (primary) exit node.
    await user.click(screen.getByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: vnet.
    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet-dc1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toEqual([
      {
        op: "sdn.zone.create",
        target: "sdn-zone::dc-evpn",
        params: {
          type: "evpn",
          controller: "evpn1",
          nodes: ["pve1", "pve2", "pve3"],
          exitNodes: ["pve1"],
          peers: ["10.10.0.11", "10.10.0.12", "10.10.0.13"],
        },
      },
      {
        op: "sdn.vnet.create",
        target: "sdn-vnet::dc-evpn/vnet-dc1",
        params: { zone: "dc-evpn", vlanAware: false },
      },
      { op: "sdn.apply", params: {} },
    ]);
  }, 15000);
});

describe("EvpnZoneWizard — T-403 AC3 (3-peer BGP session graph)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the review step's session-graph summary for a 3-peer input, naming controller and every peer", async () => {
    const user = userEvent.setup();
    stubWizardFetch();
    renderWithProviders(<EvpnZoneWizard open onOpenChange={() => undefined} />);

    await fillControllerStep(user);
    await user.click(screen.getByRole("button", { name: "Next" })); // exit nodes step, none picked
    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet-dc1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Review step names the controller and all three peers — the textual
    // half of "renders the resulting BGP session graph"; the same data
    // (previewEntities.buildEvpnPreview) also drives the live preview
    // pane's actual graph, unit-tested exhaustively in
    // previewEntities.test.ts's own 3-peer AC3 case.
    expect(screen.getByText(/Referencing controller "evpn1"/)).toBeInTheDocument();
    expect(screen.getByText((_, el) => el?.textContent === "Peers: 10.10.0.11, 10.10.0.12, 10.10.0.13")).toBeInTheDocument();
  }, 15000);
});
