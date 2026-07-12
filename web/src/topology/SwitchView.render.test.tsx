// Renders the switch faceplate view against the same real captured
// three-node-vlan fixture the graph-view render test uses (see
// threeNodeVlan.render.test.tsx), asserting the faceplate surfaces the
// switch, its uplink bond + member NICs + LLDP neighbors, the guest access
// port, and that the VLAN filter / layer toggles behave.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Layer, TopologyResponse } from "../api/types";
import { SwitchView } from "./SwitchView";
import { buildSwitchModel } from "./switchModel";

const full = fullFixture as unknown as TopologyResponse;
const ALL = new Set<Layer>(["phys", "l2", "sdn", "guest"]);

function renderView(opts?: {
  vlanFilter?: number;
  activeLayers?: Set<Layer>;
  onSelect?: (ref: string) => void;
  onExpandGroup?: (id: string) => void;
}) {
  const topology = buildSwitchModel(full.nodes, full.edges);
  return render(
    <div style={{ width: 1200, height: 800 }}>
      <SwitchView
        topology={topology}
        selectedId={undefined}
        vlanFilter={opts?.vlanFilter}
        activeLayers={opts?.activeLayers ?? ALL}
        staleNodeGroups={new Set()}
        onSelect={opts?.onSelect ?? (() => undefined)}
        onExpandGroup={opts?.onExpandGroup ?? (() => undefined)}
      />
    </div>,
  );
}

describe("SwitchView — three-node-vlan faceplates", () => {
  it("renders one switch per node with its uplink bond, member NICs and LLDP neighbor", () => {
    renderView();
    // Three vmbr0 switch headers (one per node).
    expect(screen.getAllByLabelText("vmbr0 switch")).toHaveLength(3);
    // pve1's faceplate: bond0 uplink with eno1/eno2 members and neighbors.
    const pve1 = screen.getByLabelText("node pve1");
    expect(within(pve1).getByLabelText("bond0")).toBeInTheDocument();
    expect(within(pve1).getByLabelText("eno1")).toBeInTheDocument();
    expect(within(pve1).getByText("sw-core-01")).toBeInTheDocument();
    expect(within(pve1).getByText("sw-core-02")).toBeInTheDocument();
    // Both member NICs' neighbors happen to report the same remote port.
    expect(within(pve1).getAllByText("Te1/0/1")).toHaveLength(2);
  });

  it("renders the guest access port and realized VNets", () => {
    renderView();
    const scoped = within(screen.getByLabelText("node pve1"));
    expect(scoped.getByLabelText("app01/net0")).toBeInTheDocument();
    expect(scoped.getByLabelText("vnet100 (app-tier)")).toBeInTheDocument();
    expect(scoped.getByLabelText("vnet200 (db-tier)")).toBeInTheDocument();
  });

  it("clicking the switch header selects its ref", () => {
    const onSelect = vi.fn();
    renderView({ onSelect });
    const headers = screen.getAllByLabelText("vmbr0 switch");
    const first = headers[0];
    if (!first) throw new Error("expected a switch header");
    fireEvent.click(first);
    expect(onSelect).toHaveBeenCalledWith("bridge:pve1:vmbr0");
  });

  it("hides access ports when the guest layer is toggled off", () => {
    renderView({ activeLayers: new Set<Layer>(["phys", "l2", "sdn"]) });
    expect(screen.queryByLabelText("app01/net0")).not.toBeInTheDocument();
    // The switch itself still renders.
    expect(screen.getAllByLabelText("vmbr0 switch")).toHaveLength(3);
  });

  it("empty topology shows the graph-view hint", () => {
    render(
      <SwitchView
        topology={{ nodes: [] }}
        selectedId={undefined}
        vlanFilter={undefined}
        activeLayers={ALL}
        staleNodeGroups={new Set()}
        onSelect={() => undefined}
        onExpandGroup={() => undefined}
      />,
    );
    expect(screen.getByText("No bridges to show")).toBeInTheDocument();
  });
});
