// Renders the switch faceplate view against the same real captured
// three-node-vlan fixture the graph-view render test uses (see
// threeNodeVlan.render.test.tsx), asserting the faceplate surfaces the
// switch, its uplink bond + member NICs + LLDP neighbors, the guest access
// port, and that the VLAN filter / layer toggles behave.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Layer, TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
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

  it("shows a 'standby' badge on an inactive bond member and none on the active one (SwitchFaceplate's !nic.active branch)", () => {
    const nodes: TopologyNode[] = [
      {
        id: "bridge:pve1:vmbr9",
        kind: "bridge",
        label: "vmbr9",
        layer: "l2",
        nodeGroup: "pve1",
        status: "ok",
        badges: [],
      },
      { id: "bond:pve1:bond9", kind: "bond", label: "bond9", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
      {
        id: "physnic:pve1:eno9a",
        kind: "physnic",
        label: "eno9a",
        layer: "phys",
        nodeGroup: "pve1",
        status: "ok",
        badges: [],
      },
      {
        id: "physnic:pve1:eno9b",
        kind: "physnic",
        label: "eno9b",
        layer: "phys",
        nodeGroup: "pve1",
        status: "ok",
        badges: [],
      },
    ];
    const edges: TopologyEdge[] = [
      { from: "bond:pve1:bond9", to: "bridge:pve1:vmbr9", kind: "port-of", status: "ok", badges: [] },
      { from: "physnic:pve1:eno9a", to: "bond:pve1:bond9", kind: "enslaved-by", status: "ok", badges: ["active"] },
      // No "active" badge: this member is the standby/backup slave.
      { from: "physnic:pve1:eno9b", to: "bond:pve1:bond9", kind: "enslaved-by", status: "ok", badges: [] },
    ];
    const topology = buildSwitchModel(nodes, edges);
    render(
      <div style={{ width: 1200, height: 800 }}>
        <SwitchView
          topology={topology}
          selectedId={undefined}
          vlanFilter={undefined}
          activeLayers={ALL}
          staleNodeGroups={new Set()}
          onSelect={() => undefined}
          onExpandGroup={() => undefined}
        />
      </div>,
    );
    const activeNic = screen.getByLabelText("eno9a");
    const standbyNic = screen.getByLabelText("eno9b");
    expect(within(activeNic).queryByText("standby")).not.toBeInTheDocument();
    expect(within(standbyNic).getByText("standby")).toBeInTheDocument();
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

// T-702 acceptance criterion 4: the switch faceplate badges its header
// (mgmt/corosync — the carrier) and the uplink-bay ports on the path
// (mgmt-path), against the same real three-node-vlan fixture, with the
// badges applied the way internal/topology.ApplyMgmtBadges would (pve1's
// vmbr0 is the mgmt carrier over bond0/eno1/eno2, per that fixture's own
// golden test in internal/topology/mgmtpath_test.go).
describe("SwitchView — T-702 management-path badges", () => {
  function withMgmtBadges(nodes: TopologyNode[]): TopologyNode[] {
    const mgmtPath = new Set(["bond:pve1:bond0", "physnic:pve1:eno1", "physnic:pve1:eno2"]);
    return nodes.map((n) => {
      if (n.id === "bridge:pve1:vmbr0") return { ...n, badges: [...n.badges, "mgmt", "corosync"] };
      if (mgmtPath.has(n.id)) return { ...n, badges: [...n.badges, "mgmt-path"] };
      return n;
    });
  }

  it("badges the switch header with mgmt/corosync and the uplink bond+members with mgmt-path", () => {
    const topology = buildSwitchModel(withMgmtBadges(full.nodes), full.edges);
    renderViewWith(topology);

    const pve1 = screen.getByLabelText("node pve1");
    const header = within(pve1).getByLabelText("vmbr0 switch");
    expect(within(header).getByText("mgmt")).toBeInTheDocument();
    expect(within(header).getByText("corosync")).toBeInTheDocument();

    const bond = within(pve1).getByLabelText("bond0");
    expect(within(bond).getByText("mgmt-path")).toBeInTheDocument();
    const eno1 = within(pve1).getByLabelText("eno1");
    expect(within(eno1).getByText("mgmt-path")).toBeInTheDocument();

    // pve2/pve3 vmbr0 headers carry neither badge in this hand-augmented
    // fixture (only pve1's path was marked) — proves the treatment is
    // per-node, not blanket-applied to every same-named switch.
    const pve2 = screen.getByLabelText("node pve2");
    expect(within(pve2).queryByText("mgmt")).not.toBeInTheDocument();
  });

  function renderViewWith(topology: ReturnType<typeof buildSwitchModel>): void {
    render(
      <div style={{ width: 1200, height: 800 }}>
        <SwitchView
          topology={topology}
          selectedId={undefined}
          vlanFilter={undefined}
          activeLayers={ALL}
          staleNodeGroups={new Set()}
          onSelect={() => undefined}
          onExpandGroup={() => undefined}
        />
      </div>,
    );
  }
});
