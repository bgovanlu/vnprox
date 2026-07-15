import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { VlanZoneWizard } from "./VlanZoneWizard";
import { renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

const NAME_FIELD = /^Name/;

// The wizard now opens pre-filled with click-through defaults (zone name,
// VLAN-aware bridge, all nodes selected). fillTrunkStep overrides the name to
// a test-specific value and leaves the auto-selected nodes in place, so the
// existing golden/LLDP assertions still read clearly.
async function fillTrunkStep(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
  const nameField = screen.getByRole("textbox", { name: NAME_FIELD });
  await user.clear(nameField);
  await user.type(nameField, "prodnet");
  // Bridge is pre-filled to vmbr0 (the default); nodes pve1/pve2/pve3 are
  // auto-selected. Both are what these tests want, so leave them.
  await user.click(screen.getByRole("button", { name: "Next" }));
}

async function setVid(user: ReturnType<typeof userEvent.setup>, vid: string): Promise<void> {
  const vidField = screen.getByRole("spinbutton", { name: /^VLAN ID/ });
  await user.clear(vidField);
  await user.type(vidField, vid);
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
    await setVid(user, "100");
    const vnetField = screen.getByRole("textbox", { name: /^VNet name/ });
    await user.clear(vnetField);
    await user.type(vnetField, "vnet100");
    await waitFor(() => { expect(screen.getByRole("status")).toHaveTextContent(/Looks good/); });
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: accept the default subnet.
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };

    const zoneOp = ops.find((op) => op.op === "sdn.zone.create");
    expect(zoneOp?.target).toBe("sdn-zone::prodnet");
    expect(zoneOp?.params).toMatchObject({ type: "vlan", bridge: "vmbr0", nodes: ["pve1", "pve2", "pve3"] });

    const vnetOp = ops.find((op) => op.op === "sdn.vnet.create");
    expect(vnetOp?.target).toBe("sdn-vnet::prodnet/vnet100");
    expect(vnetOp?.params).toMatchObject({ zone: "prodnet", tag: 100 });

    // The default subnet is drafted too (click-through defaults).
    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp?.params).toMatchObject({ vnet: "prodnet/vnet100", cidr: "10.10.10.0/24" });

    expect(ops.some((op) => op.op === "sdn.apply")).toBe(true);
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
    await setVid(user, "300");

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
    await setVid(user, "100");

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(/Looks good/);
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  }, 15000);
});
