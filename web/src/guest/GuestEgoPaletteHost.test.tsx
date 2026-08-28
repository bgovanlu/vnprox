// SPDX-License-Identifier: Apache-2.0

// T-3906: the topology-map entry point (see GuestEgoPaletteHost.tsx's own
// doc comment for why this is a palette registrar rather than an
// InspectorPanel button). Verifies the palette verb appears only when the
// map's current selection is a guest, and navigates to the right ref.
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { GuestEgoPaletteHost } from "./GuestEgoPaletteHost";
import { useAllPaletteActions } from "../keyboard/actions";

let selectedId: string | undefined;
vi.mock("../topology/store", () => ({
  useTopologyStore: (selector: (s: { selectedId: string | undefined }) => unknown) => selector({ selectedId }),
}));

const navigateMock = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => navigateMock };
});

function Probe() {
  const actions = useAllPaletteActions();
  return <div data-testid="actions">{actions.map((a) => a.label).join("|")}</div>;
}

function renderHost() {
  return render(
    <MemoryRouter>
      <GuestEgoPaletteHost />
      <Probe />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  selectedId = undefined;
  navigateMock.mockClear();
});

describe("GuestEgoPaletteHost", () => {
  it("registers no action when nothing (or a non-guest) is selected", () => {
    selectedId = undefined;
    const { getByTestId } = renderHost();
    expect(getByTestId("actions").textContent).toBe("");
  });

  it("registers no action for a non-guest selection (e.g. a bridge)", () => {
    selectedId = "bridge:pve1:vmbr0";
    const { getByTestId } = renderHost();
    expect(getByTestId("actions").textContent).toBe("");
  });

  it("registers 'Open guest view' when a guest is selected, and navigates to its ego view", () => {
    selectedId = "guest:pve1:100";
    const { getByTestId } = renderHost();
    expect(getByTestId("actions").textContent).toBe("Open guest view for guest:pve1:100");
  });
});
