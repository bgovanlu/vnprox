// T-3004 AC3: QoS edits stage a changeset, and no direct write path exists.
//
// The second half is asserted structurally, against the shipped source
// rather than against a comment: `api/qos.ts` is the only module that may
// talk to `/qos/*`, and it must contain no mutating call. A future agent
// adding a POST there fails this test rather than a review.
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  buildQosShapeCreateOp,
  buildQosShapeDeleteOp,
  buildQosShapeUpdateOp,
  qosShapeFormChanged,
  qosShapeRef,
  type QosShapeFormValues,
} from "./qosOps";

const SRC = resolve(__dirname, "..");

function form(overrides: Partial<QosShapeFormValues> = {}): QosShapeFormValues {
  return { bridge: "vmbr0", rateMbit: 100, ...overrides };
}

describe("qos op builders", () => {
  it("targets a qos-shape Ref carrying the owning node", () => {
    expect(qosShapeRef("pve1", "guest-egress")).toBe("qos-shape:pve1:guest-egress");
  });

  it("builds a create op with the bridge as a plain interface name", () => {
    const op = buildQosShapeCreateOp("pve1", "guest-egress", form({ ceilMbit: 200, matchVlan: 20 }));
    expect(op).toEqual({
      op: "qos.shape.create",
      target: "qos-shape:pve1:guest-egress",
      params: { bridge: "vmbr0", rateMbit: 100, ceilMbit: 200, matchCidr: undefined, matchVlan: 20, priority: undefined },
    });
  });

  it("omits an empty match CIDR rather than sending an empty string", () => {
    const op = buildQosShapeCreateOp("pve1", "s1", form({ matchCidr: "" }));
    expect(op.params).toMatchObject({ matchCidr: undefined });
  });

  it("builds an update op carrying only the changed fields", () => {
    const initial = form({ ceilMbit: 200, priority: 3 });
    const op = buildQosShapeUpdateOp("pve1", "s1", initial, { ...initial, rateMbit: 250 });
    expect(op.op).toBe("qos.shape.update");
    expect(op.params).toEqual({ rateMbit: 250 });
  });

  it("builds a delete op with no params — the target Ref is the whole input", () => {
    expect(buildQosShapeDeleteOp("pve1", "s1")).toEqual({
      op: "qos.shape.delete",
      target: "qos-shape:pve1:s1",
      params: {},
    });
  });

  it("detects a no-op edit so nothing empty gets staged", () => {
    const initial = form({ ceilMbit: 200 });
    expect(qosShapeFormChanged(initial, { ...initial })).toBe(false);
    expect(qosShapeFormChanged(initial, { ...initial, priority: 1 })).toBe(true);
  });
});

describe("no direct QoS write path", () => {
  const qosClient = readFileSync(join(SRC, "api/qos.ts"), "utf8");

  it("the QoS API client makes no mutating call", () => {
    // Anti-vacuity: the file must actually be the QoS client, or a rename
    // would make every assertion below pass over nothing.
    expect(qosClient).toContain("/qos/shapes");

    for (const method of ['method: "POST"', 'method: "PUT"', 'method: "PATCH"', 'method: "DELETE"']) {
      expect(qosClient).not.toContain(method);
    }
    // apiFetch infers POST from a `json:` body, so an absent explicit method
    // is not on its own proof of a read.
    expect(qosClient).not.toMatch(/\bjson:/);
    expect(qosClient).not.toContain("csrfToken");
  });

  it("nothing outside the changeset client addresses a /qos write", () => {
    // Every write in this app goes through api/changesets.ts. If a QoS write
    // route ever appears, it will appear as a second module naming /qos with
    // a mutating method — which is what this catches.
    const files = ["api/qos.ts", "analysis/qosOps.ts", "analysis/QosShapesPanel.tsx"];
    for (const rel of files) {
      const source = readFileSync(join(SRC, rel), "utf8");
      expect(source, `${rel} must not call apiFetch against a QoS route`).not.toMatch(
        /apiFetch<[^>]*>\(\s*[`"']\/qos[^`"']*[`"']\s*,/,
      );
    }
  });

  it("the panel reaches the change engine through the drawer, not the API", () => {
    const panel = readFileSync(join(SRC, "analysis/QosShapesPanel.tsx"), "utf8");
    expect(panel).toContain("useDrawerActions");
    expect(panel).toContain("buildQosShapeCreateOp");
    expect(panel).toContain("buildQosShapeUpdateOp");
    expect(panel).toContain("buildQosShapeDeleteOp");
    // No route client other than the read is imported at all.
    expect(panel).not.toContain('from "../api/client"');
  });
});
