import { describe, expect, it } from "vitest";
import type { Capabilities, MeResponse } from "../api/types";
import { capsForNode, missingCapTooltip } from "./capabilities";

const fullCaps: Capabilities = {
  netRead: true,
  netWrite: true,
  sdnRead: true,
  sdnWrite: true,
  fwRead: true,
  fwWrite: true,
  guestNet: true,
  audit: true,
};

const readOnlyCaps: Capabilities = { ...fullCaps, netWrite: false, sdnWrite: false, fwWrite: false, guestNet: false };

function session(caps: Record<string, Capabilities>): MeResponse {
  return { user: { username: "auditor", realm: "pve" }, caps };
}

describe("capsForNode", () => {
  it("returns every flag false when there is no session at all", () => {
    const caps = capsForNode(undefined, "pve1");
    expect(Object.values(caps).every((v) => v === false)).toBe(true);
  });

  it("resolves the exact node's entry when present", () => {
    const s = session({ pve1: fullCaps, pve2: readOnlyCaps });
    expect(capsForNode(s, "pve1")).toEqual(fullCaps);
    expect(capsForNode(s, "pve2")).toEqual(readOnlyCaps);
  });

  it("falls back to the '' cluster-wide entry only when the exact node has none", () => {
    const s = session({ "": fullCaps });
    expect(capsForNode(s, "pve3")).toEqual(fullCaps);
  });

  it("never inherits an unrelated node's grant when the node is simply unknown and there's no '' fallback", () => {
    const s = session({ pve1: fullCaps });
    const caps = capsForNode(s, "pve9");
    expect(caps.netWrite).toBe(false);
  });
});

describe("missingCapTooltip", () => {
  it("is undefined when the session already holds the capability", () => {
    const s = session({ pve1: fullCaps });
    expect(missingCapTooltip(s, "pve1", "netWrite")).toBeUndefined();
  });

  it("names the missing privilege and node when the capability is absent", () => {
    const s = session({ pve1: readOnlyCaps });
    const msg = missingCapTooltip(s, "pve1", "netWrite");
    expect(msg).toContain("Sys.Modify");
    expect(msg).toContain("pve1");
  });

  it("phrases a cluster-scoped (empty-node) object without naming a node", () => {
    const s = session({ "": readOnlyCaps });
    const msg = missingCapTooltip(s, "", "sdnWrite");
    expect(msg).toContain("cluster-wide");
  });
});
