import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { VxlanZoneWizard } from "./VxlanZoneWizard";
import { bridgeAddress, renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

const NAME_FIELD = /^Name/;

describe("VxlanZoneWizard — T-403 AC1 (golden ops) + peer auto-suggest", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("completed against pvemock-shaped ops: produces the exact golden changeset, using auto-suggested peers", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<VxlanZoneWizard open onOpenChange={() => undefined} />);

    // Step 1: peers. All nodes are auto-selected; override the name.
    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    const nameField = screen.getByRole("textbox", { name: NAME_FIELD });
    await user.clear(nameField);
    await user.type(nameField, "overlay1");

    // Peer addresses are auto-suggested from each node's own bridge
    // address (docs/features/sdn.md §2) — wait for all three to populate.
    await waitFor(() => {
      expect(screen.getByDisplayValue(bridgeAddress("pve1").split("/")[0] ?? "")).toBeInTheDocument();
      expect(screen.getByDisplayValue(bridgeAddress("pve2").split("/")[0] ?? "")).toBeInTheDocument();
      expect(screen.getByDisplayValue(bridgeAddress("pve3").split("/")[0] ?? "")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: vnet + vni. VNI defaults to 100; set it to 300 for this test.
    const vnetField = screen.getByRole("textbox", { name: /^VNet name/ });
    await user.clear(vnetField);
    await user.type(vnetField, "vnetovl1");
    fireEvent.change(screen.getByRole("spinbutton", { name: /^VNI/ }), { target: { value: "300" } });
    expect(screen.getByTestId("vxlan-mtu-math")).toHaveTextContent(
      "1500 (underlying network MTU) − 50 (VXLAN's wrapper overhead) = 1450",
    );
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: accept the default subnet.
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };

    const zoneOp = ops.find((op) => op.op === "sdn.zone.create");
    expect(zoneOp?.target).toBe("sdn-zone::overlay1");
    expect(zoneOp?.params).toMatchObject({
      type: "vxlan",
      nodes: ["pve1", "pve2", "pve3"],
      peers: ["10.10.0.11", "10.10.0.12", "10.10.0.13"],
    });

    const vnetOp = ops.find((op) => op.op === "sdn.vnet.create");
    expect(vnetOp?.target).toBe("sdn-vnet::overlay1/vnetovl1");
    expect(vnetOp?.params).toMatchObject({ zone: "overlay1", tag: 300 });

    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp?.params).toMatchObject({ vnet: "overlay1/vnetovl1", cidr: "10.10.10.0/24" });

    expect(ops.some((op) => op.op === "sdn.apply")).toBe(true);
  }, 15000);
});

describe("VxlanZoneWizard — T-403 AC3 (MTU math + one-click fix)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("warns and offers a one-click fix when the entered MTU leaves no headroom", async () => {
    const user = userEvent.setup();
    stubWizardFetch();
    renderWithProviders(<VxlanZoneWizard open onOpenChange={() => undefined} />);

    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    const nameField = screen.getByRole("textbox", { name: NAME_FIELD });
    await user.clear(nameField);
    await user.type(nameField, "overlay1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // VNet name defaults to vnet1 — accept it and exercise the MTU warning.
    const mtuField = screen.getByRole("spinbutton", { name: /^Zone MTU/ });
    fireEvent.change(mtuField, { target: { value: "1500" } });

    expect(screen.getByText(/leaves no room for VXLAN's wrapper/)).toBeInTheDocument();
    expect(screen.getByText(/Use 1450 or lower/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Set this network's MTU to the safe value" }));

    await waitFor(() => {
      expect((mtuField as HTMLInputElement).value).toBe("1450");
    });
    expect(screen.getByRole("status")).toHaveTextContent(/leaves enough room/);
  }, 15000);
});
