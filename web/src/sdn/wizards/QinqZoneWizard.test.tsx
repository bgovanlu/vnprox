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

    // Step 1: trunk.
    await user.type(screen.getByRole("textbox", { name: NAME_FIELD }), "tenant-net");
    await user.type(screen.getByRole("textbox", { name: /^VLAN-aware bridge/ }), "vmbr0");
    await user.click(await screen.findByRole("checkbox", { name: "pve1" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 2: double tag.
    fireEvent.change(screen.getByRole("spinbutton", { name: /^Service/ }), { target: { value: "100" } });
    await user.type(screen.getByRole("spinbutton", { name: /^Customer/ }), "42");
    await user.type(screen.getByRole("textbox", { name: /^VNet name/ }), "vnet42");
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 3: subnet (skip).
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Step 4: review + finish.
    // The service tag is illustration-only — the review step says so.
    expect(screen.getByText(/Service tag 100 is shown for illustration only/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create draft" }));

    await waitFor(() => { expect(stub.postedChangesets).toHaveLength(1); });
    const { ops } = stub.postedChangesets[0] ?? { ops: [] };
    expect(ops).toEqual([
      {
        op: "sdn.zone.create",
        target: "sdn-zone::tenant-net",
        params: { type: "qinq", bridge: "vmbr0", nodes: ["pve1"] },
      },
      {
        op: "sdn.vnet.create",
        target: "sdn-vnet::tenant-net/vnet42",
        params: { zone: "tenant-net", tag: 42, vlanAware: false },
      },
      { op: "sdn.apply", params: {} },
    ]);
  }, 15000);
});
