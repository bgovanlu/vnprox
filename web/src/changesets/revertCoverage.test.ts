// T-1805 (UI half): the pure logic behind the two statements the interface
// must make about whether a changeset will undo itself — the review screen's
// pre-apply notice and the countdown banner's live coverage line.
import { describe, expect, it } from "vitest";
import type { Changeset, Op } from "../api/types";
import {
  changesetNeedsRevertTicket,
  opNeedsRevertTicket,
  preApplyRevertNotice,
  revertCoverageBanner,
  revertTicketOpFamilies,
} from "./revertCoverage";

const fwOp: Op = { op: "fw.rule.create", target: "fw-ruleset:pve1:guest/qemu/100", params: { direction: "in", action: "DROP" } };
const sdnZoneOp: Op = { op: "sdn.zone.create", target: "sdn-zone::z1", params: { type: "simple" } };
const sdnApplyOp: Op = { op: "sdn.apply", params: {} };
const bridgeOp: Op = { op: "bridge.create", target: "bridge:pve1:vmbr9", params: {} };
const wgOp: Op = { op: "wg.tunnel.create", target: "wg-tunnel:pve1:wg0", params: {} };

describe("opNeedsRevertTicket — mirrors internal/change's Plan.needsRevertTicket", () => {
  const cases: { op: Op; want: boolean; why: string }[] = [
    { op: fwOp, want: true, why: "firewall writes go out under the user's PVE ticket" },
    { op: sdnZoneOp, want: true, why: "SDN stage ops are cluster-scope PVE API calls" },
    { op: sdnApplyOp, want: true, why: "sdn.apply is the same family, named explicitly by T-502's report" },
    { op: bridgeOp, want: false, why: "node-file ops are written by the daemon as root" },
    { op: wgOp, want: false, why: "the WireGuard gateway is daemon-level" },
  ];
  for (const c of cases) {
    it(`${c.op.op} -> ${String(c.want)} (${c.why})`, () => {
      expect(opNeedsRevertTicket(c.op)).toBe(c.want);
    });
  }

  it("classifies a changeset by whether ANY op needs the ticket", () => {
    expect(changesetNeedsRevertTicket([bridgeOp, wgOp])).toBe(false);
    expect(changesetNeedsRevertTicket([bridgeOp, fwOp])).toBe(true);
    expect(changesetNeedsRevertTicket([])).toBe(false);
  });

  it("names the ticket-scoped families present, for plain copy", () => {
    expect(revertTicketOpFamilies([fwOp])).toEqual(["firewall"]);
    expect(revertTicketOpFamilies([sdnApplyOp])).toEqual(["SDN"]);
    expect(revertTicketOpFamilies([fwOp, sdnZoneOp])).toEqual(["firewall", "SDN"]);
    expect(revertTicketOpFamilies([bridgeOp])).toEqual([]);
  });
});

describe("preApplyRevertNotice — the review screen's statement before apply", () => {
  it("says nothing for a changeset whose revert needs no ticket", () => {
    expect(preApplyRevertNotice([bridgeOp, wgOp], 120).show).toBe(false);
  });

  it("states plainly, for a firewall changeset, what is kept and for how long", () => {
    const notice = preApplyRevertNotice([fwOp], 120);
    expect(notice.show).toBe(true);
    expect(notice.heading).toContain("firewall");
    expect(notice.body).toContain("120 seconds");
    // The credential's whole lifecycle is stated: kept, scoped, deleted.
    expect(notice.body).toContain("encrypted copy");
    expect(notice.body).toContain("only to undo this one changeset");
    expect(notice.body).toContain("deletes it the moment you confirm");
  });

  it("warns that the ticket can expire first, and names the consequence", () => {
    const notice = preApplyRevertNotice([sdnZoneOp, sdnApplyOp], 600);
    expect(notice.body).toContain("about 2 hours");
    expect(notice.body).toContain("no longer undo itself automatically");
    expect(notice.body).toContain("by hand");
  });

  it("tracks the confirm window the operator actually chose", () => {
    expect(preApplyRevertNotice([fwOp], 180).body).toContain("180 seconds");
    expect(preApplyRevertNotice([fwOp], 1).body).toContain("1 second");
  });

  it("says which parts are NOT affected when the changeset is mixed", () => {
    const mixed = preApplyRevertNotice([bridgeOp, fwOp], 120);
    expect(mixed.body).toContain("per-node interface changes in this changeset are not affected");
    // ...and does not claim that for a firewall-only changeset, where it
    // would be a meaningless reassurance.
    expect(preApplyRevertNotice([fwOp], 120).body).not.toContain("are not affected");
  });

  it("names both families when both are present", () => {
    const notice = preApplyRevertNotice([fwOp, sdnApplyOp], 120);
    expect(notice.heading).toContain("firewall and SDN");
  });
});

function awaiting(overrides: Partial<Changeset>): Changeset {
  return {
    id: "cs1",
    title: "t",
    author: "root@pam",
    status: "awaiting_confirm",
    ops: [fwOp],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

describe("revertCoverageBanner — the in-window statement, from the server's report", () => {
  const nowMs = 1_700_000_000_000;

  it("says nothing when the server sent no report (older server, or out of window)", () => {
    expect(revertCoverageBanner(awaiting({}), nowMs).show).toBe(false);
  });

  it("says nothing when nothing in the changeset needs a ticket", () => {
    const cs = awaiting({ unattendedRevert: { required: false, available: true, fullWindow: true } });
    expect(revertCoverageBanner(cs, nowMs).show).toBe(false);
  });

  it("confirms full coverage when the ticket outlives the window", () => {
    const cs = awaiting({
      unattendedRevert: { required: true, available: true, fullWindow: true, coversUntil: 1_700_000_120 },
    });
    const out = revertCoverageBanner(cs, nowMs);
    expect(out).toMatchObject({ show: true, tone: "ok" });
    expect(out.text).toContain("will undo itself too");
  });

  it("counts down the reduced coverage when the ticket expires first", () => {
    const cs = awaiting({
      unattendedRevert: { required: true, available: true, fullWindow: false, coversUntil: 1_700_000_045 },
      confirmDeadline: 1_700_000_600,
    });
    const out = revertCoverageBanner(cs, nowMs);
    expect(out).toMatchObject({ show: true, tone: "partial" });
    expect(out.text).toContain("stops working in 45s");
  });

  it("reports lapsed coverage once the ticket's cut-off has passed", () => {
    const cs = awaiting({
      unattendedRevert: { required: true, available: true, fullWindow: false, coversUntil: 1_699_999_900 },
    });
    const out = revertCoverageBanner(cs, nowMs);
    expect(out.tone).toBe("partial");
    expect(out.text).toContain("has already lapsed");
  });

  it("states plainly, and prominently, when there is no automatic undo at all", () => {
    const cs = awaiting({
      unattendedRevert: {
        required: true,
        available: false,
        fullWindow: false,
        reason: "no PVE session credential was available at apply time; firewall/SDN changes in this changeset will not revert automatically",
      },
    });
    const out = revertCoverageBanner(cs, nowMs);
    expect(out).toMatchObject({ show: true, tone: "none" });
    expect(out.text).toContain("will NOT undo itself");
    expect(out.text).toContain("will not revert automatically");
    expect(out.text).toContain("by hand");
  });
});
