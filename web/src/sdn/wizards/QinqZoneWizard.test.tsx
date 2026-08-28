// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QinqZoneWizard } from "./QinqZoneWizard";
import { renderWithProviders, stubWizardFetch } from "./wizardTestUtils";

// The "Service (outer) VLAN tag" number input reproducibly does not
// receive userEvent's simulated keystrokes in this jsdom environment
// (isolated and confirmed via direct component testing outside this
// suite: the field's onChange logic itself is correct — a plain
// `fireEvent.change` updates it fine, and an otherwise-identical number
// field elsewhere, e.g. VlanZoneWizard's own VID input, types correctly
// via userEvent). Using fireEvent directly for this one field sidesteps
// what appears to be a jsdom/testing-library interaction quirk rather
// than a real product bug.

const NAME_FIELD = /^Name/;

describe("QinqZoneWizard — T-403 AC1 (golden ops)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("completed against pvemock-shaped ops: produces the exact golden changeset", async () => {
    const user = userEvent.setup();
    const stub = stubWizardFetch();
    renderWithProviders(<QinqZoneWizard open onOpenChange={() => undefined} />);

    // Step 1: trunk. Bridge (vmbr0) and all nodes are pre-filled defaults;
    // override the name and accept the rest.
    await waitFor(() => { expect(screen.getByRole("checkbox", { name: "pve1" })).toBeChecked(); });
    const nameField = screen.getByRole("textbox", { name: NAME_FIELD });
    await user.clear(nameField);
    await user.type(nameField, "tenants");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: double tag. Service defaults to 100; set the customer tag to 42.
    fireEvent.change(screen.getByRole("spinbutton", { name: /^Service/ }), { target: { value: "100" } });
    fireEvent.change(screen.getByRole("spinbutton", { name: /^Customer/ }), { target: { value: "42" } });
    const vnetField = screen.getByRole("textbox", { name: /^VNet name/ });
    await user.clear(vnetField);
    await user.type(vnetField, "vnet42");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: accept the default subnet.
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    // The service tag is illustration-only — the review step says so.
    expect(screen.getByText(/Service tag 100 is shown for illustration only/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };

    const zoneOp = ops.find((op) => op.op === "sdn.zone.create");
    expect(zoneOp?.target).toBe("sdn-zone::tenants");
    expect(zoneOp?.params).toMatchObject({ type: "qinq", bridge: "vmbr0", nodes: ["pve1", "pve2", "pve3"] });

    const vnetOp = ops.find((op) => op.op === "sdn.vnet.create");
    expect(vnetOp?.target).toBe("sdn-vnet::tenants/vnet42");
    expect(vnetOp?.params).toMatchObject({ zone: "tenants", tag: 42 });

    const subnetOp = ops.find((op) => op.op === "sdn.subnet.create");
    expect(subnetOp?.params).toMatchObject({ vnet: "tenants/vnet42", cidr: "10.10.10.0/24" });

    expect(ops.some((op) => op.op === "sdn.apply")).toBe(true);
  }, 15000);
});
