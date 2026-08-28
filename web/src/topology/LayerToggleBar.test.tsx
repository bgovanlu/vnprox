// SPDX-License-Identifier: Apache-2.0

// T-1003 AC3: the "Flows" overlay toggle (rendered by LayerToggleBar, see
// its own doc comment for why it lives here rather than as a 5th `Layer`)
// and the pre-existing "Traffic" paint-mode button (a sibling control in
// TopologyPage.tsx's toolbar — see its own render block right after
// <LayerToggleBar/>) must both be independently toggleable, present, and
// visually distinct at once — this test mounts the exact same two controls
// side by side (the real components, not a stand-in) the way TopologyPage
// composes them, and drives both to "on" simultaneously.
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Layer } from "../api/types";
import { Button } from "../components/Button";
import { LayerToggleBar } from "./LayerToggleBar";

const LAYER_ORDER: readonly Layer[] = ["phys", "l2", "sdn", "guest"];

/** Mirrors TopologyPage.tsx's toolbar row: LayerToggleBar (now carrying the
 * Flows toggle) plus the standalone Traffic-mode Button right after it. */
function ToolbarHarness() {
  const [activeLayers, setActiveLayers] = useState<Set<Layer>>(new Set(LAYER_ORDER));
  const [trafficMode, setTrafficMode] = useState(false);
  const [flowsLayerActive, setFlowsLayerActive] = useState(false);

  return (
    <div>
      <LayerToggleBar
        activeLayers={activeLayers}
        layerOrder={LAYER_ORDER}
        onToggle={(layer) => {
          setActiveLayers((prev) => {
            const next = new Set(prev);
            if (next.has(layer)) next.delete(layer);
            else next.add(layer);
            return next;
          });
        }}
        flowsLayerActive={flowsLayerActive}
        onToggleFlows={() => { setFlowsLayerActive((v) => !v); }}
      />
      <Button
        size="sm"
        variant={trafficMode ? "primary" : "secondary"}
        aria-pressed={trafficMode}
        onClick={() => { setTrafficMode((v) => !v); }}
      >
        Traffic
      </Button>
    </div>
  );
}

describe("Flows layer toggle + Traffic paint-mode button coexist (T-1003 AC3)", () => {
  it("both controls render, independently, alongside the base 4-layer rail", () => {
    render(<ToolbarHarness />);
    for (const layer of ["Physical", "L2", "SDN", "Guests"]) {
      expect(screen.getByRole("button", { name: new RegExp(layer) })).toBeInTheDocument();
    }
    expect(screen.getByRole("button", { name: "Flows" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Traffic" })).toBeInTheDocument();
  });

  it("both start off, and can both be switched on at once without affecting each other", async () => {
    const user = userEvent.setup();
    render(<ToolbarHarness />);

    const flowsBtn = screen.getByRole("button", { name: "Flows" });
    const trafficBtn = screen.getByRole("button", { name: "Traffic" });
    expect(flowsBtn).toHaveAttribute("aria-pressed", "false");
    expect(trafficBtn).toHaveAttribute("aria-pressed", "false");

    await user.click(flowsBtn);
    expect(flowsBtn).toHaveAttribute("aria-pressed", "true");
    // Turning Flows on must not toggle Traffic (distinct, independent state).
    expect(trafficBtn).toHaveAttribute("aria-pressed", "false");

    await user.click(trafficBtn);
    expect(trafficBtn).toHaveAttribute("aria-pressed", "true");
    // Both now on simultaneously — neither control disappeared or merged.
    expect(flowsBtn).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Flows" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Traffic" })).toBeInTheDocument();
  });

  it("toggling a base layer (e.g. Guests) never flips the Flows toggle", async () => {
    const user = userEvent.setup();
    render(<ToolbarHarness />);
    const flowsBtn = screen.getByRole("button", { name: "Flows" });
    await user.click(screen.getByRole("button", { name: /Guests/ }));
    expect(flowsBtn).toHaveAttribute("aria-pressed", "false");
  });

  it("omitting onToggleFlows renders the original 4-button-only rail (backward compatible)", () => {
    render(
      <LayerToggleBar activeLayers={new Set(LAYER_ORDER)} layerOrder={LAYER_ORDER} onToggle={() => undefined} />,
    );
    expect(screen.queryByRole("button", { name: "Flows" })).not.toBeInTheDocument();
  });
});
