import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { VlanZoneWizard } from "./VlanZoneWizard";
import { renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

const NAME_FIELD = /^Name/;

async function fillTrunkStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "prodnet");
  await user.type(screen.getByRole("textbox", { name: /^VLAN-aware bridge/ }), "vmbr0");
  await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
  await user.click(screen.getByRole("checkbox", { name: "pve2" }));
  await user.click(screen.getByRole("checkbox", { name: "pve3" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
}

describe("VlanZoneWizard — T-403 AC1 (golden ops)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("completed against pvemock-shaped ops: produces the exact golden changeset", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<VlanZoneWizard open onOpenChange={() => undefined} />);

    await fillTrunkStep(user);

    // Step 2: VID 100 is trunked everywhere in the fixture (clean check).
    await user.type(screen.getByRole("spinbutton", { name: /^VLAN ID/ }), "100");
    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet100");
    await waitFor(() => { expect(screen.getByRole("status")).toHaveTextContent(/Looks good/); });
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: subnet (skip).
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toEqual([
      {
        op: "sdn.zone.create",
        target: "sdn-zone::prodnet",
        params: { type: "vlan", bridge: "vmbr0", nodes: ["pve1", "pve2", "pve3"] },
      },
      {
        op: "sdn.vnet.create",
        target: "sdn-vnet::prodnet/vnet100",
        params: { zone: "prodnet", tag: 100, vlanAware: false },
      },
      { op: "sdn.apply", params: {} },
    ]);
  }, 15000);
});

describe("VlanZoneWizard — T-403 AC2 (LLDP trunk cross-check)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("warns, naming the switch port, when a member node's trunk is missing the VID", async () => {
    const user = userEvent.setup();
    stubWizardFetch();
    renderWithProviders(<VlanZoneWizard open onOpenChange={() => undefined} />);

    await fillTrunkStep(user);

    // VID 300 is missing on pve3's trunk in the shared test fixture
    // (wizardTestUtils.ts's threeNodeTopology/inventoryFixture) — the same
    // "pve3 missing VID 300" scenario testdata/clusters/three-node-vlan.yaml
    // seeds for the backend.
    await user.type(screen.getByRole("spinbutton", { name: /^VLAN ID/ }), "300");

    const alerts = await screen.findAllByRole("alert", {}, { timeout: 3000 });
    const warningTexts = alerts.map((el) => el.textContent).filter((t) => t.includes("VLAN 300"));
    expect(warningTexts.length).toBeGreaterThan(0);
    // Names the port, per AC2.
    expect(warningTexts.some((t) => t.includes("Te1/0/3"))).toBe(true);
    expect(warningTexts.some((t) => t.includes("sw-core-01"))).toBe(true);
  }, 15000);

  it("no warning when every member's trunk carries the VID", async () => {
    const user = userEvent.setup();
    stubWizardFetch();
    renderWithProviders(<VlanZoneWizard open onOpenChange={() => undefined} />);

    await fillTrunkStep(user);
    await user.type(screen.getByRole("spinbutton", { name: /^VLAN ID/ }), "100");

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(/Looks good/);
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  }, 15000);
});
