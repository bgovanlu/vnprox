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

    // Step 1: peers.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "overlay1");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("checkbox", { name: "pve2" }));

    // Peer addresses are auto-suggested from each node's own bridge
    // address (docs/features/sdn.md §2) — wait for both to populate.
    await waitFor(() => {
      expect(screen.getByDisplayValue(bridgeAddress("pve1").split("/")[0] ?? "")).toBeInTheDocument();
      expect(screen.getByDisplayValue(bridgeAddress("pve2").split("/")[0] ?? "")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: vnet + mtu (leave mtu blank — the derivation shows 1450 as
    // the safe default without the field needing a value).
    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet-overlay1");
    fireEvent.change(screen.getByRole("spinbutton", { name: /^VNI/ }), { target: { value: "300" } });
    expect(screen.getByTestId("vxlan-mtu-math")).toHaveTextContent(
      "1500 (underlying network MTU) − 50 (VXLAN's wrapper overhead) = 1450",
    );
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
        target: "sdn-zone::overlay1",
        params: { type: "vxlan", nodes: ["pve1", "pve2"], peers: ["10.10.0.11", "10.10.0.12"] },
      },
      {
        op: "sdn.vnet.create",
        target: "sdn-vnet::overlay1/vnet-overlay1",
        params: { zone: "overlay1", tag: 300, vlanAware: false },
      },
      { op: "sdn.apply", params: {} },
    ]);
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

    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "overlay1");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet1");
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
