// SPDX-License-Identifier: Apache-2.0

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

describe("SimpleZoneWizard — click-through defaults + golden ops", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("accepting every default and clicking through drafts a complete SDN (zone + all nodes + vnet + subnet)", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={() => undefined} />);

    // Step 1: the zone name is pre-filled and every node is auto-selected
    // once the node list loads — the step is valid with no input.
    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    expect(screen.getByRole("textbox", { name: NAME_FIELD })).toHaveValue("homelab");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: vnet name pre-filled.
    expect(screen.getByRole("textbox", { name: NAME_FIELD })).toHaveValue("vnet1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: subnet pre-filled with a default CIDR + gateway.
    expect(screen.getByRole("textbox", { name: /^Address range/ })).toHaveValue("10.10.10.0/24");
    expect(screen.getByRole("textbox", { name: /^Gateway/ })).toHaveValue("10.10.10.1");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: finish — no field was ever touched.
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };

    const zoneOp = ops.find((op) => op.op === "sdn.zone.create");
    expect(zoneOp?.target).toBe("sdn-zone::homelab");
    expect(zoneOp?.params).toMatchObject({ type: "simple", nodes: ["pve1", "pve2", "pve3"] });

    const vnetOp = ops.find((op) => op.op === "sdn.vnet.create");
    expect(vnetOp?.target).toBe("sdn-vnet::homelab/vnet1");

    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp?.target).toBe("sdn-subnet::10.10.10.0/24");
    expect(subnetOp?.params).toMatchObject({ vnet: "homelab/vnet1", cidr: "10.10.10.0/24", gateway: "10.10.10.1" });

    expect(ops.some((op) => op.op === "sdn.apply")).toBe(true);
  }, 15000);

  it("editing the CIDR re-fills the gateway; SNAT is disabled until a gateway is set — T-701 AC1", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<SimpleZoneWizard open onOpenChange={() => undefined} />);

    // Accept the pre-filled zone + vnet steps (nodes auto-selected).
    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: replace the default CIDR — editing it re-derives the gateway.
    const cidrField = screen.getByRole("textbox", { name: /^Address range/ });
    await user.clear(cidrField);
    await user.type(cidrField, "10.50.0.0/24");
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

    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    const cidrField = screen.getByRole("textbox", { name: /^Address range/ });
    await user.clear(cidrField);
    await user.type(cidrField, "10.50.0.0/24");
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

    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    await user.click(screen.getByRole("button", { name: "Next" }));

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(stub.postedChangesets).toHaveLength(0);
  }, 15000);
});
