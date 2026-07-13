import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SimpleZoneWizard } from "./SimpleZoneWizard";
import { wizardStrings } from "./strings";
import { renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

const S = wizardStrings;

// Field's accessible name (implicit <label> wrapping both the label text
// and the inline help paragraph) includes the help text too — every query
// below anchors on the label's own leading text with `/^Label/` rather
// than an exact match, so it's robust to that help text without needing a
// full string match.
const NAME_FIELD = /^Name/;
const ALIAS_FIELD = /^Alias/;

describe("SimpleZoneWizard — T-403 AC1 (golden ops) + AC5 (no draft residue)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("completed against pvemock-shaped ops: produces the exact golden changeset", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={() => undefined} />);

    // Step 1: zone.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "homelab");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("checkbox", { name: "pve2" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: vnet.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "vnet1");
    await user.type(screen.getByRole("textbox", { name: ALIAS_FIELD }), "guest-lan");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: subnet (skip — leave CIDR blank).
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toEqual([
      {
        op: "sdn.zone.create",
        target: "sdn-zone::homelab",
        params: { type: "simple", nodes: ["pve1", "pve2"] },
      },
      {
        op: "sdn.vnet.create",
        target: "sdn-vnet::homelab/vnet1",
        params: { zone: "homelab", alias: "guest-lan", vlanAware: false },
      },
      { op: "sdn.apply", params: {} },
    ]);
  }, 15000);

  it("typing a CIDR pre-fills the gateway; SNAT is disabled until a gateway is set — T-701 AC1", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={() => undefined} />);

    // Step 1: zone.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "homelab");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: vnet.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "vnet1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: subnet — typing the CIDR pre-fills the gateway to the first
    // usable address.
    await user.type(screen.getByRole("textbox", { name: /^Address range/ }), "10.50.0.0/24");
    const gatewayField = await screen.findByRole("textbox", { name: /^Gateway/ });
    expect(gatewayField).toHaveValue("10.50.0.1");

    const snatCheckbox = screen.getByRole("checkbox", { name: /Enable SNAT/ });
    expect(snatCheckbox).not.toBeDisabled();

    // Clearing the gateway disables SNAT (with a reason); typing it back in
    // re-enables it.
    await user.clear(gatewayField);
    expect(snatCheckbox).toBeDisabled();
    await user.type(gatewayField, "10.50.0.1");
    expect(snatCheckbox).not.toBeDisabled();
    await user.click(snatCheckbox);

    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp).toEqual({
      op: "sdn.subnet.create",
      target: "sdn-subnet::10.50.0.0/24",
      params: { vnet: "homelab/vnet1", cidr: "10.50.0.0/24", gateway: "10.50.0.1", snat: true },
    });
  }, 15000);

  it("choosing 'keep isolated' drafts a subnet with no gateway and no snat, and never re-fills it — T-701 AC1", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={() => undefined} />);

    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "homelab");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "vnet1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    await user.type(screen.getByRole("textbox", { name: /^Address range/ }), "10.50.0.0/24");
    await screen.findByRole("textbox", { name: /^Gateway/ }); // pre-filled first

    await user.click(screen.getByRole("radio", { name: S.common.gatewayModeIsolated }));
    expect(screen.queryByRole("textbox", { name: /^Gateway/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /Enable SNAT/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp).toEqual({
      op: "sdn.subnet.create",
      target: "sdn-subnet::10.50.0.0/24",
      params: { vnet: "homelab/vnet1", cidr: "10.50.0.0/24", snat: false },
    });
  }, 15000);

  it("abandoning mid-wizard (Cancel) never drafts anything — AC5", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    const onOpenChange = vi.fn();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={onOpenChange} />);

    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "abandoned");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "vnetX");

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(stub.postedChangesets).toHaveLength(0);
  }, 15000);
});
