// SPDX-License-Identifier: Apache-2.0

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

// T-2004: a design-system pass over SwitchView/SwitchFaceplate found several
// 9-10px labels that were sub-AA against the surfaces they actually render
// on — some by a hair (text-amber-700 on bg-amber-100 measured 4.52:1, just
// over the 4.5:1 floor with no headroom), some outright (text-teal-500 on
// bg-teal-50 measured 2.32:1). The worst offender was a copy-paste pattern,
// `text-slate-400 dark:text-slate-400` — identical in both themes, so it
// only ever cleared AA against a dark card; every light-mode instance
// measured ~2.4-2.6:1 against this app's actual light-mode surfaces
// (bg-slate-100 page background / bg-white card / bg-indigo-50 header).
// These tests assert the corrected class strings directly (jsdom doesn't
// compute real CSS colour, so a rendered-pixel contrast assertion isn't
// available here — the actual ratios are computed and recorded in the
// component comments and this task's report) and guard against regressing
// back to the broken pairing.
describe("SwitchView / SwitchFaceplate — T-2004 contrast fixes", () => {
  it("standby badge uses the status-info token, not amber (T-4204: informational, not a warning)", () => {
    const nodes: TopologyNode[] = [
      { id: "bridge:pve1:vmbr9", kind: "bridge", label: "vmbr9", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
      { id: "bond:pve1:bond9", kind: "bond", label: "bond9", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
      { id: "physnic:pve1:eno9a", kind: "physnic", label: "eno9a", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
      { id: "physnic:pve1:eno9b", kind: "physnic", label: "eno9b", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
    ];
    const edges: TopologyEdge[] = [
      { from: "bond:pve1:bond9", to: "bridge:pve1:vmbr9", kind: "port-of", status: "ok", badges: [] },
      { from: "physnic:pve1:eno9a", to: "bond:pve1:bond9", kind: "enslaved-by", status: "ok", badges: ["active"] },
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
    const badge = screen.getByText("standby");
    expect(badge.className).toContain("text-status-info");
    expect(badge.className).not.toContain("amber");
  });

  it("a stale node group's 'stale' pill uses the status-stale token, not a filled amber badge (T-4204)", () => {
    const topology = buildSwitchModel(full.nodes, full.edges);
    render(
      <div style={{ width: 1200, height: 800 }}>
        <SwitchView
          topology={topology}
          selectedId={undefined}
          vlanFilter={undefined}
          activeLayers={ALL}
          staleNodeGroups={new Set(["pve1"])}
          onSelect={() => undefined}
          onExpandGroup={() => undefined}
        />
      </div>,
    );
    const badge = screen.getByText("stale");
    expect(badge.className).toContain("text-status-stale");
    expect(badge.className).not.toContain("text-amber");
    expect(badge.className).not.toContain("bg-status-stale");
  });

  it("the node group's switch-count label uses slate-600/slate-400, not the light-mode-broken slate-400/slate-400 pairing (2.4:1 against bg-slate-100)", () => {
    renderView();
    const [label] = screen.getAllByText(/^\d+ switch(es)?$/);
    if (!label) throw new Error("expected a switch-count label");
    // T-4215: was `text-slate-600 dark:text-slate-400` spelled out. That pair
    // IS `--color-fg-muted` (#475569/#94a3b8, byte-identical in both themes),
    // so the conversion changed no pixel — but it did change what this test
    // should assert. Pinning the token rather than the two steps keeps the
    // guard pointed at the property (this label is legible in light mode,
    // where the old slate-400/slate-400 pairing measured 2.4:1) instead of at
    // one spelling of it.
    expect(label.className).toContain("text-fg-muted");
    expect(label.className).not.toContain("text-slate-400");
  });

  // T-3503 rewrote the chassis header from a light `bg-indigo-50` bar into a
  // dark name plate, which invalidates T-2004's original assertion here
  // rather than regressing it: `text-slate-600` was the fix for slate-400
  // sitting on indigo-50 (2.35:1), and on a slate-800 plate slate-600 would
  // now be the *failing* value (2.1:1) while the old slate-400 would pass.
  // The pairing under test therefore changes with the surface; what does not
  // change is that the kind marking must clear AA in both themes, so that is
  // what this now measures. slate-300 on the plate: 10.4:1 light
  // (bg-slate-800), 12.9:1 dark (bg-slate-950), and 7.6:1 against the
  // hover:bg-slate-700 state — all well clear of the 4.5:1 floor at this
  // 9px size, with the margin T-2004 asked for rather than the ~4.5:1
  // squeakers it was filed over.
  it("the chassis kind marking clears AA against the dark name plate in both themes", () => {
    renderView();
    const [kindLabel] = screen.getAllByText("bridge");
    if (!kindLabel) throw new Error("expected a chassis kind label");
    expect(kindLabel.className).toContain("text-slate-300");
    // The pre-T-3503 pairing would be sub-AA on this surface — guard against
    // a well-meaning revert reintroducing it.
    expect(kindLabel.className).not.toContain("text-slate-600");
  });

  it("the access port's NIC-key text and VLAN id marker both clear AA in light mode (were 2.63:1 and 4.4:1)", () => {
    renderView();
    const port = screen.getByLabelText("app01/net0");
    // Located by content, not by child index: T-3503 wrapped the access
    // port's contents in a layout span (the port cell), which silently
    // shifted every index-based lookup here by one level. The colours are
    // what this test is about; the DOM nesting is not, and pinning the
    // nesting only made a faceplate redesign look like a contrast
    // regression.
    const nicKeySpan = within(port).getByText("net0");
    expect(nicKeySpan.className).toContain("text-fg-muted"); // was text-slate-600, T-4215
    expect(nicKeySpan.className).not.toContain("text-slate-400");
    const vidMarker = within(port).getByText("·100");
    expect(vidMarker.className).toContain("text-violet-700");
    expect(vidMarker.className).not.toContain("text-violet-500");
  });

  it("the VNet tag's ·<vlan> suffix uses teal-700, not teal-500 (measured 2.32:1 against bg-teal-50)", () => {
    renderView();
    const vnetButton = within(screen.getByLabelText("node pve1")).getByLabelText("vnet100 (app-tier)");
    // Structure (VNet button in SwitchFaceplate.tsx): [0] LED, [1] label,
    // [2] the "·<tag>" suffix (only present when v.tag is defined).
    const tagSpan = vnetButton.children[2];
    if (!(tagSpan instanceof HTMLElement)) throw new Error("expected the vnet tag suffix span");
    expect(tagSpan.textContent).toContain("100");
    expect(tagSpan.className).toContain("text-teal-700");
    expect(tagSpan.className).not.toContain("text-teal-500");
  });

  it("a free (unattached) port's Section label and bond/nic type tag clear AA in light mode (both were ~2.4-2.6:1)", () => {
    const nodes: TopologyNode[] = [
      // SwitchView renders its "No bridges to show" empty state when there
      // are zero switches at all, so this needs a real bridge alongside the
      // genuinely-unattached NIC below.
      { id: "bridge:pve1:vmbr9", kind: "bridge", label: "vmbr9", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
      { id: "physnic:pve1:eno9a", kind: "physnic", label: "eno9a", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
      { id: "physnic:pve1:enofree", kind: "physnic", label: "enofree", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
    ];
    const edges: TopologyEdge[] = [
      { from: "physnic:pve1:eno9a", to: "bridge:pve1:vmbr9", kind: "port-of", status: "ok", badges: [] },
      // enofree has no edges at all — never consumed, so it surfaces under
      // "Unattached ports" via SwitchView's freeByNode/FreePort path.
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
    const sectionLabel = screen.getByText("Unattached ports");
    expect(sectionLabel.className).toContain("text-fg-muted"); // was text-slate-600, T-4215
    expect(sectionLabel.className).not.toContain("text-slate-400");

    const tag = screen.getByText("nic");
    expect(tag.className).toContain("text-fg-muted"); // was the slate pair, T-4215
  });
});
