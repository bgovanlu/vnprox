// SPDX-License-Identifier: Apache-2.0

// T-3503: the two pure decisions behind a drawn port — which jack, and what
// speed marking. Kept out of the render tests because they are the part that
// can be wrong *silently*: a faceplate that draws an SFP cage for a copper
// port still renders, still passes axe, and still looks like a switch.
import { describe, expect, it } from "vitest";
import { bodyForNic, jackKindForEntity, speedMarking } from "./portMedia";

describe("bodyForNic", () => {
  // The kernel PORT_* vocabulary internal/host maps into inventory
  // (linux/ethtool.h). Every value that reaches the wire is listed, so a new
  // one added backend-side without a decision here shows up as a failing
  // case rather than as a silently-defaulted RJ45.
  const cases: { media: string | undefined; want: ReturnType<typeof bodyForNic>; why: string }[] = [
    { media: "tp", want: "rj45", why: "PORT_TP — every NIC on the reference node reads this" },
    { media: "mii", want: "rj45", why: "PORT_MII is copper" },
    { media: "aui", want: "rj45", why: "PORT_AUI is copper" },
    { media: "bnc", want: "rj45", why: "PORT_BNC is copper" },
    { media: "fibre", want: "sfp", why: "PORT_FIBRE — the cage case" },
    { media: "da", want: "sfp", why: "PORT_DA (direct attach) also lives in a cage" },
    { media: "none", want: "unknown", why: "PORT_NONE is the driver declining to say" },
    { media: "other", want: "unknown", why: "PORT_OTHER is the driver declining to say" },
    { media: "", want: "unknown", why: "no reading — failed or unattempted ioctl" },
    { media: undefined, want: "unknown", why: "field absent — a peer node never host-polled" },
    { media: "TP", want: "unknown", why: "case-sensitive: the wire is lowercase, anything else is unrecognised" },
    { media: "twisted pair", want: "unknown", why: "the ethtool CLI's human text is not the wire value" },
  ];
  for (const c of cases) {
    it(`${String(c.media)} -> ${c.want} (${c.why})`, () => {
      expect(bodyForNic(c.media)).toBe(c.want);
    });
  }

  it("never infers a body from anything but the reported media type", () => {
    // The regression this pins: a "10G must be fibre" shortcut. 10GBASE-T is
    // copper, and the reference node's own 100M/1G ports are copper too, so
    // speed carries no media information at all. bodyForNic's signature not
    // taking a speed is the enforcement; this asserts the intent so a future
    // change that adds one has to delete a test that says why not.
    expect(bodyForNic.length).toBe(1);
  });
});

describe("speedMarking", () => {
  it.each([
    [10, "10M"],
    [100, "100M"],
    [1000, "1G"],
    [2500, "2.5G"],
    [10000, "10G"],
    [25000, "25G"],
    [100000, "100G"],
  ])("%i Mbps renders as %s", (mbps, want) => {
    expect(speedMarking(mbps)).toBe(want);
  });

  it("renders nothing for a link with no reported speed", () => {
    // The case the evidence transcript is about: pvecube's enp2s0/enp4s0
    // have no carrier, so the kernel reports no speed and the projection
    // omits the field. A faceplate must print no marking — not "0M", and
    // not a remembered figure from when the cable was in.
    expect(speedMarking(undefined)).toBeUndefined();
    expect(speedMarking(0)).toBeUndefined();
    // -1 is what /sys/class/net/<if>/speed literally contains on a down
    // link. internal/topology guards it off the wire; if that guard ever
    // slips, this must still not render "-1M".
    expect(speedMarking(-1)).toBeUndefined();
  });
});

// T-3505: which jack silhouette (if any) an entity's kind draws. One
// function, shared by all three renderers (EntityNode.tsx's v1 DOM node,
// canvasDraw.ts's v2 canvas, SwitchFaceplate.tsx's faceplate) — the two
// views disagreeing about what an entity *is* would be worse than either
// being wrong alone, which is exactly what a per-renderer copy of this
// mapping invites.
describe("jackKindForEntity", () => {
  it("picks the jack body from a physnic's reported media port, mirroring portMedia.ts's bodyForNic", () => {
    expect(jackKindForEntity("physnic", "tp")).toBe("rj45");
    expect(jackKindForEntity("physnic", "fibre")).toBe("sfp");
    expect(jackKindForEntity("physnic", "da")).toBe("sfp");
  });

  it("draws an unreported physnic media port as 'unknown', never a guessed rj45 (T-3503's rule, applied here too)", () => {
    expect(jackKindForEntity("physnic", undefined)).toBe("unknown");
    expect(jackKindForEntity("physnic", "")).toBe("unknown");
    expect(jackKindForEntity("physnic", "other")).toBe("unknown");
  });

  it("gives every guest-nic the dashed virtual jack, independent of any mediaPort value (guest NICs never carry one)", () => {
    expect(jackKindForEntity("guest-nic", undefined)).toBe("virtual");
  });

  it("gives every other kind no jack at all — bridges, bonds, and everything else keep their plain box", () => {
    for (const kind of ["bridge", "ovs-bridge", "bond", "ovs-bond", "vlan", "guest", "sdn-zone", "guest-group", "phys-group"]) {
      expect(jackKindForEntity(kind, "tp")).toBeUndefined();
    }
  });
});
