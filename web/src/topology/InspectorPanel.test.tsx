// SPDX-License-Identifier: Apache-2.0

// Inspector panel: the F-08 "Raw source" tab now renders GET
// /inventory/{ref}'s real `rawSource` map (docs/api.md), provenance moved
// to its own tab, and the fields view applies the tri-state `*Set` rule
// (a false LinkUpSet/VlanAwareSet/STPSet means "not reported" → render
// "unknown", never a fabricated false).
//
// The fixture (__fixtures__/inventory-detail-vmbr0.json) was captured from
// a live vnproxd+pvemock (three-node-vlan) GET /inventory/bridge:pve1:vmbr0
// — its pve-network rawSource, provenance, and STPSet:false are all real —
// then hand-extended with a "host-interfaces" stanza entry consistent with
// docs/api.md's pinned shape (a dev machine outside a PVE node has no real
// vmbr0 stanza to capture; noted for hardware validation).
import detailFixture from "./__fixtures__/inventory-detail-vmbr0.json";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { fieldRows } from "./fields";
import { InspectorPanel } from "./InspectorPanel";

const detail = detailFixture as unknown as EntityDetail;

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn(() => Promise.resolve(mockDetailResponse)),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

let mockDetailResponse: EntityDetail = detail;

/** The <dt> label's row wrapper (the <div class="contents"> holding the
 * dt/dd pair), for scoping value assertions to one field's row. */
function rowOf(label: HTMLElement): HTMLElement {
  const parent = label.parentElement;
  if (parent === null) throw new Error("field label has no row wrapper");
  return parent;
}

function renderPanel(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      {/* ToastProvider: the panel's T-207 Edit/Delete affordances use
       * useToast, which requires the provider (mounted app-wide in main.tsx).
       * MemoryRouter: T-306's FDB tab deep-links via useNavigate. */}
      <MemoryRouter>
        <ToastProvider>
          <InspectorPanel selectedRef={detail.ref} onClose={() => undefined} onSelectRelated={() => undefined} />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("fieldRows (tri-state *Set rule)", () => {
  it("renders a field whose *Set companion is false as 'unknown', not false", () => {
    const rows = new Map(fieldRows({ STP: false, STPSet: false }));
    expect(rows.get("STP")).toBe("unknown");
  });

  it("renders a field whose *Set companion is true as its actual value", () => {
    const rows = new Map(fieldRows({ VlanAware: true, VlanAwareSet: true, LinkUp: false, LinkUpSet: true }));
    expect(rows.get("VlanAware")).toBe("true");
    expect(rows.get("LinkUp")).toBe("false");
  });

  it("never lists the *Set companion keys as rows of their own", () => {
    const keys = fieldRows({ STP: false, STPSet: false, VlanAware: true, VlanAwareSet: true }).map(([k]) => k);
    expect(keys).toEqual(["STP", "VlanAware"]);
  });

  it("leaves a field that merely ends in 'Set' (no base field) alone", () => {
    const rows = new Map(fieldRows({ RuleSet: "strict" }));
    expect(rows.get("RuleSet")).toBe("strict");
  });

  it("still drops empty values", () => {
    const keys = fieldRows({ Name: "vmbr0", Gateway: "", Vids: null }).map(([k]) => k);
    expect(keys).toEqual(["Name"]);
  });
});

describe("InspectorPanel", () => {
  it("applies the *Set rule in the fields tab (captured fixture: STPSet is false)", async () => {
    mockDetailResponse = detail;
    renderPanel();
    const stpLabel = await screen.findByText("STP");
    // The <dt>/<dd> pair share a wrapper div; the value cell must read
    // "unknown" even though the wire value is `false`.
    expect(within(rowOf(stpLabel)).getByText("unknown")).toBeInTheDocument();
    // VlanAwareSet is true, so VlanAware renders its real value.
    const vlanAwareLabel = screen.getByText("VlanAware");
    expect(within(rowOf(vlanAwareLabel)).getByText("true")).toBeInTheDocument();
    // The *Set companions themselves are not listed.
    expect(screen.queryByText("STPSet")).not.toBeInTheDocument();
  });

  it("renders one raw-source section per source on the Raw source tab", async () => {
    mockDetailResponse = detail;
    renderPanel();
    await screen.findByText("STP"); // wait for the query to resolve
    await userEvent.click(screen.getByRole("tab", { name: "Raw source" }));

    // The verbatim interfaces(5) stanza, in a monospace <pre>.
    expect(screen.getByRole("heading", { name: "host-interfaces" })).toBeInTheDocument();
    expect(screen.getByText(/iface vmbr0 inet static/)).toBeInTheDocument();
    // The PVE API object's pretty-printed JSON, as captured.
    expect(screen.getByRole("heading", { name: "pve-network" })).toBeInTheDocument();
    expect(screen.getByText(/"bridge_vlan_aware": true/)).toBeInTheDocument();
  });

  it("keeps per-field provenance visible on its own tab", async () => {
    mockDetailResponse = detail;
    renderPanel();
    await screen.findByText("STP");
    await userEvent.click(screen.getByRole("tab", { name: "Provenance" }));
    expect(screen.getByText("vlanAware")).toBeInTheDocument();
    expect(screen.getAllByText(/pve-network/).length).toBeGreaterThan(0);
  });

  it("handles an entity with no raw source gracefully", async () => {
    const { rawSource: _rawSource, ...rest } = detail;
    mockDetailResponse = rest;
    renderPanel();
    await screen.findByText("STP");
    await userEvent.click(screen.getByRole("tab", { name: "Raw source" }));
    expect(screen.getByText(/No raw source retained for this entity/)).toBeInTheDocument();
  });
});
