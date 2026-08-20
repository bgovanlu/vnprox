// T-3503: the faceplate as a device model. Three things are under test here
// that the pre-existing SwitchView.render.test.tsx does not cover:
//
//   1. the reference node renders recognisably as switches with populated
//      ports (AC1) — against a topology captured from the live deployment on
//      pvecube, not against a hand-written fixture;
//   2. a port's drawn body and speed marking follow the observed facts, and
//      go absent rather than guessing when those facts are missing;
//   3. the port field survives a density the reference node cannot reach
//      (AC2), and every port in it stays reachable by keyboard.
//
// On the fixture: `__fixtures__/pvecube-reference-topology.json` is a verbatim
// `GET /topology` capture from the deployed daemon on 2026-08-20 (PVE 9.2.4,
// six bridges, five guests, four physical NICs). It predates the speedMbps/
// mediaPort fields this task added backend-side, so every physnic in it
// carries neither — which makes it the honest "we have no reading" case, and
// means AC1 is proved against data that has never been curated to make the
// renderer look good. The speed/media rendering is proved separately below,
// on synthetic nodes shaped like what the projection now emits.
import pvecubeFixture from "./__fixtures__/pvecube-reference-topology.json";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Layer, TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { SwitchView } from "./SwitchView";
import { buildSwitchModel } from "./switchModel";

const ALL = new Set<Layer>(["phys", "l2", "sdn", "guest"]);

function renderTopology(nodes: TopologyNode[], edges: TopologyEdge[]) {
  return render(
    <div style={{ width: 1200, height: 800 }}>
      <SwitchView
        topology={buildSwitchModel(nodes, edges)}
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

/** A drawn jack, identified by the shape PortBody.tsx gives it. The RJ45 body
 * is the only one with a keying-tab path; the SFP cage is the only one built
 * from <rect>s; the unknown body is the only dashed rect. Asserting on the
 * geometry rather than on a data-* attribute keeps the test honest: it fails
 * if the drawing changes into something that is no longer that connector. */
function jackKind(button: HTMLElement): "rj45" | "sfp" | "unknown" | "none" {
  const svg = button.querySelector("svg");
  if (!svg) return "none";
  const paths = Array.from(svg.querySelectorAll("path"));
  if (paths.some((p) => (p.getAttribute("d") ?? "").includes("h22 v11 h-7 v5"))) return "rj45";
  const rects = Array.from(svg.querySelectorAll("rect"));
  if (rects.length >= 2) return "sfp";
  if (rects.some((r) => r.getAttribute("stroke-dasharray") !== null)) return "unknown";
  return "none";
}

describe("T-3503 AC1 — the reference node renders as switches with populated ports", () => {
  const fixture = pvecubeFixture as unknown as TopologyResponse;

  it("draws all six of pvecube's bridges as chassis", () => {
    renderTopology(fixture.nodes, fixture.edges);
    for (const name of ["vmbr0", "vmbr1", "vmbr2", "vmbr3", "fwbr103i0", "fwbr104i0"]) {
      expect(screen.getByLabelText(`${name} switch`)).toBeInTheDocument();
    }
  });

  it("populates vmbr0 with its real uplink and its three guest access ports", () => {
    renderTopology(fixture.nodes, fixture.edges);
    const node = screen.getByLabelText("node pvecube");
    // enp1s0 is vmbr0's only uplink on the real node — the very fact the
    // health check `mgmt_single_path` fires about.
    expect(within(node).getByLabelText("enp1s0")).toBeInTheDocument();
    // Three guests are on vmbr0: opnsense/net0, librenms/net0, powerdns/net0.
    for (const label of ["opnsense/net0", "librenms/net0", "powerdns/net0"]) {
      expect(within(node).getByLabelText(label)).toBeInTheDocument();
    }
  });

  it("prints each port's identity at rest, without hover or selection", () => {
    renderTopology(fixture.nodes, fixture.edges);
    const node = screen.getByLabelText("node pvecube");
    // The NIC name is rendered text inside the port cell, not only an
    // aria-label — "legible at rest" is a T-3503 deliverable in as many
    // words, and an accessible name alone does not satisfy it.
    expect(within(within(node).getByLabelText("enp1s0")).getByText("enp1s0")).toBeInTheDocument();
    // A guest access port prints its VMID (the port number) and guest name.
    const opnsense = within(node).getByLabelText("opnsense/net0");
    expect(within(opnsense).getByText("102")).toBeInTheDocument();
    expect(within(opnsense).getByText("opnsense")).toBeInTheDocument();
  });

  it("reflects each guest NIC's real link state — opnsense net1/net2/net3 are down", () => {
    renderTopology(fixture.nodes, fixture.edges);
    const node = screen.getByLabelText("node pvecube");
    for (const label of ["opnsense/net1", "opnsense/net2", "opnsense/net3"]) {
      const port = within(node).getByLabelText(label);
      const desc = document.getElementById(port.getAttribute("aria-describedby") ?? "");
      // Status in words, not only in the LED's hue — the down state must
      // survive both a greyscale (stale) faceplate and a colour-vision
      // deficiency.
      expect(desc?.textContent).toContain("status down");
    }
    // ...and the ports that are up say so, so the assertion above is not
    // passing because everything says "down".
    const up = within(node).getByLabelText("librenms/net0");
    expect(document.getElementById(up.getAttribute("aria-describedby") ?? "")?.textContent).toContain("status ok");
  });

  it("draws the honest unknown-media body when the capture carries no reading", () => {
    // Every physnic in this capture predates the mediaPort field. The
    // faceplate must not default them to a confident RJ45.
    renderTopology(fixture.nodes, fixture.edges);
    const nic = within(screen.getByLabelText("node pvecube")).getByLabelText("enp1s0");
    expect(jackKind(nic)).toBe("unknown");
    const desc = document.getElementById(nic.getAttribute("aria-describedby") ?? "");
    expect(desc?.textContent).toContain("media type unknown");
    // ...and no invented speed.
    expect(desc?.textContent).not.toContain("link speed");
  });
});

describe("T-3503 — a port is drawn from what was observed, never from what was inferred", () => {
  function nicTopology(nic: Partial<TopologyNode>): [TopologyNode[], TopologyEdge[]] {
    const bridge: TopologyNode = {
      id: "bridge:pve1:vmbr0",
      kind: "bridge",
      label: "vmbr0",
      layer: "l2",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    };
    const port: TopologyNode = {
      id: "physnic:pve1:eno1",
      kind: "physnic",
      label: "eno1",
      layer: "phys",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
      ...nic,
    };
    return [
      [bridge, port],
      [{ from: port.id, to: bridge.id, kind: "port-of", status: "ok", badges: [] }],
    ];
  }

  it("copper reads as an RJ45 jack with its negotiated speed silkscreened", () => {
    const [nodes, edges] = nicTopology({ mediaPort: "tp", speedMbps: 1000 });
    renderTopology(nodes, edges);
    const nic = screen.getByLabelText("eno1");
    expect(jackKind(nic)).toBe("rj45");
    expect(within(nic).getByText("1G")).toBeInTheDocument();
    expect(document.getElementById(nic.getAttribute("aria-describedby") ?? "")?.textContent).toContain(
      "copper RJ45, link speed 1G",
    );
  });

  it("fibre reads as an SFP cage", () => {
    const [nodes, edges] = nicTopology({ mediaPort: "fibre", speedMbps: 10000 });
    renderTopology(nodes, edges);
    const nic = screen.getByLabelText("eno1");
    expect(jackKind(nic)).toBe("sfp");
    expect(within(nic).getByText("10G")).toBeInTheDocument();
  });

  it("a down fibre port keeps its cage and simply loses its speed marking", () => {
    // The conflation the evidence transcript exists to prevent: media is a
    // property of the socket and survives link loss, speed is a property of
    // the link and does not. A port that changed shape when its cable was
    // pulled would be telling the operator the hardware had changed.
    const [nodes, edges] = nicTopology({ mediaPort: "fibre", status: "down" });
    renderTopology(nodes, edges);
    const nic = screen.getByLabelText("eno1");
    expect(jackKind(nic)).toBe("sfp");
    expect(within(nic).queryByText(/^\d+(\.\d+)?[MG]$/)).toBeNull();
    const desc = document.getElementById(nic.getAttribute("aria-describedby") ?? "")?.textContent ?? "";
    expect(desc).toContain("status down");
    expect(desc).toContain("fibre or direct-attach SFP");
    expect(desc).not.toContain("link speed");
  });
});

/** The bond module: the framed group the bond's own header button sits
 * inside, and which must also contain its member ports — "a joined group,
 * not adjacent independent ports" is the T-3503 deliverable, and containment
 * is how that is checked structurally rather than by eye. */
function bondModule(header: HTMLElement): HTMLElement {
  const module = header.parentElement;
  if (!module) throw new Error("expected the bond header to sit inside a module");
  return module;
}

describe("T-3503 — a bond is drawn as one joined group, not as adjacent ports", () => {
  function bondTopology(activeCount: number): [TopologyNode[], TopologyEdge[]] {
    const bridge: TopologyNode = {
      id: "bridge:pve1:vmbr0",
      kind: "bridge",
      label: "vmbr0",
      layer: "l2",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    };
    const bond: TopologyNode = {
      id: "bond:pve1:bond0",
      kind: "bond",
      label: "bond0",
      layer: "l2",
      nodeGroup: "pve1",
      status: activeCount === 2 ? "ok" : "degraded",
      badges: ["mode=802.3ad"],
    };
    const members: TopologyNode[] = ["eno1", "eno2"].map((name) => ({
      id: `physnic:pve1:${name}`,
      kind: "physnic",
      label: name,
      layer: "phys",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
      mediaPort: "fibre",
      speedMbps: 10000,
    }));
    const edges: TopologyEdge[] = [
      { from: bond.id, to: bridge.id, kind: "port-of", status: "ok", badges: [] },
      ...members.map((m, i) => ({
        from: m.id,
        to: bond.id,
        kind: "enslaved-by",
        status: "ok" as const,
        badges: i < activeCount ? ["active"] : [],
      })),
    ];
    return [[bridge, bond, ...members], edges];
  }

  it("carries the LACP member state on the group, and both members inside it", () => {
    const [nodes, edges] = bondTopology(2);
    renderTopology(nodes, edges);
    const bond = screen.getByLabelText("bond0");
    expect(within(bond).getByText("2/2 up")).toBeInTheDocument();
    // The group is a container around the member ports, not a sibling of
    // them: both member jacks live inside the bond module's own subtree.
    const module = bondModule(bond);
    expect(within(module).getByLabelText("eno1")).toBeInTheDocument();
    expect(within(module).getByLabelText("eno2")).toBeInTheDocument();
  });

  it("reports a degraded aggregation as a member count, not just an amber LED", () => {
    const [nodes, edges] = bondTopology(1);
    renderTopology(nodes, edges);
    const bond = screen.getByLabelText("bond0");
    expect(within(bond).getByText("1/2 up")).toBeInTheDocument();
    const desc = document.getElementById(bond.getAttribute("aria-describedby") ?? "")?.textContent ?? "";
    expect(desc).toContain("bond mode 802.3ad");
    expect(desc).toContain("1/2 up members");
    // The standby member says so on its own cell too (pre-existing
    // behaviour, kept: T-3503 must not lose it in the redraw).
    expect(within(bondModule(bond)).getByText("standby")).toBeInTheDocument();
  });
});

describe("T-3503 AC2 — density: 48 ports on one switch", () => {
  function densePorts(count: number): [TopologyNode[], TopologyEdge[]] {
    const bridge: TopologyNode = {
      id: "bridge:pve1:vmbr0",
      kind: "bridge",
      label: "vmbr0",
      layer: "l2",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    };
    const nodes: TopologyNode[] = [bridge];
    const edges: TopologyEdge[] = [];
    for (let i = 0; i < count; i++) {
      const vmid = 100 + i;
      const id = `guest-nic:pve1:${String(vmid)}/net0`;
      nodes.push({
        id,
        kind: "guest-nic",
        label: `guest${String(vmid)}/net0`,
        layer: "guest",
        nodeGroup: "pve1",
        status: i % 7 === 0 ? "down" : "ok",
        badges: [],
      });
      edges.push({ from: id, to: bridge.id, kind: "attached-to", status: "ok", badges: [] });
    }
    return [nodes, edges];
  }

  it("renders every one of the 48 ports — none dropped, none collapsed away", () => {
    const [nodes, edges] = densePorts(48);
    renderTopology(nodes, edges);
    for (let i = 0; i < 48; i++) {
      expect(screen.getByLabelText(`guest${String(100 + i)}/net0`)).toBeInTheDocument();
    }
  });

  it("prints the full port count in the silkscreen, so scrolling never under-reports", () => {
    const [nodes, edges] = densePorts(48);
    renderTopology(nodes, edges);
    // The count is the whole point of the scroll behaviour being safe: the
    // field is height-capped, so an operator sees ~3 rows — the label is
    // what tells them there are 48.
    expect(screen.getByText("Access ports (48)")).toBeInTheDocument();
  });

  it("caps the port field's height and scrolls it, rather than growing the chassis", () => {
    const [nodes, edges] = densePorts(48);
    renderTopology(nodes, edges);
    const field = screen.getByLabelText("guest100/net0").closest("div");
    expect(field).not.toBeNull();
    const cls = (field as HTMLElement).className;
    expect(cls).toContain("overflow-y-auto");
    expect(cls).toContain("max-h-");
    expect(cls).toContain("flex-wrap");
  });

  it("keeps every port keyboard-reachable inside the scrolled field", () => {
    const [nodes, edges] = densePorts(48);
    renderTopology(nodes, edges);
    // Ports are <button>s, so they are in the tab order and scroll into view
    // on focus — which is also what satisfies axe's scrollable-region-
    // focusable rule for the capped field without a tabindex of its own.
    for (let i = 0; i < 48; i++) {
      const port = screen.getByLabelText(`guest${String(100 + i)}/net0`);
      expect(port.tagName).toBe("BUTTON");
      expect(port.getAttribute("tabindex")).toBeNull();
    }
  });

  it("a four-port switch is unaffected by the cap", () => {
    // The cap is unconditional, so this asserts the thing that makes that
    // safe: a sparse switch's field is shorter than the cap and simply never
    // scrolls, with no count threshold at which the layout changes character.
    const [nodes, edges] = densePorts(4);
    renderTopology(nodes, edges);
    expect(screen.getByText("Access ports (4)")).toBeInTheDocument();
    for (let i = 0; i < 4; i++) {
      expect(screen.getByLabelText(`guest${String(100 + i)}/net0`)).toBeInTheDocument();
    }
  });
});
