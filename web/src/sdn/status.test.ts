// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { sdnNodeEntityStatus, sdnZoneEntityStatus } from "./status";

describe("sdnNodeEntityStatus", () => {
  it("maps ok/empty to ok", () => {
    expect(sdnNodeEntityStatus("ok")).toBe("ok");
    expect(sdnNodeEntityStatus("")).toBe("ok");
    expect(sdnNodeEntityStatus("OK")).toBe("ok");
  });
  it("maps error to down (red)", () => {
    expect(sdnNodeEntityStatus("error")).toBe("down");
  });
  it("maps pending to degraded (amber)", () => {
    expect(sdnNodeEntityStatus("pending")).toBe("degraded");
  });
  it("maps anything else to unknown", () => {
    expect(sdnNodeEntityStatus("weird")).toBe("unknown");
  });
});

describe("sdnZoneEntityStatus", () => {
  it("is unknown with no reported node status", () => {
    expect(sdnZoneEntityStatus([])).toBe("unknown");
  });
  it("is ok when every node is ok", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "ok" },
      ]),
    ).toBe("ok");
  });
  // T-401 acceptance criterion 4: a zone with a node reporting error status
  // paints amber/red consistently — down (red) specifically for "error",
  // distinct from the milder "pending" (amber) case.
  it("is down (red) when any node reports error", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "error" },
      ]),
    ).toBe("down");
  });
  it("is degraded (amber) when a node is pending but none error", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "pending" },
      ]),
    ).toBe("degraded");
  });
  it("error wins over pending when both are present", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "pending" },
        { node: "pve2", status: "error" },
      ]),
    ).toBe("down");
  });

  // T-3701: the vnprox-synthesized "unknown" status (a declared member node
  // PVE had nothing to report for at all — internal/pve.
  // ReconcileSDNZoneStatus's doc comment, confirmed live on a real two-node
  // cluster). It must move the aggregate off "ok" (silently reading a gap
  // as healthy is exactly the bug this task exists to fix), but a confirmed
  // error/pending elsewhere still outranks a mere "we don't know".
  it("is unknown (not ok) when a reporting node itself has no status to give", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "ok" },
        { node: "pve2", status: "unknown" },
      ]),
    ).toBe("unknown");
  });
  it("error wins over unknown when both are present", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "unknown" },
        { node: "pve2", status: "error" },
      ]),
    ).toBe("down");
  });
  it("pending wins over unknown when both are present", () => {
    expect(
      sdnZoneEntityStatus([
        { node: "pve1", status: "unknown" },
        { node: "pve2", status: "pending" },
      ]),
    ).toBe("degraded");
  });
});
