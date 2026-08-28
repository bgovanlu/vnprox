// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { ApiError } from "./client";
import { explainPermission, permissionExplanation } from "./permissionExplain";
import type { PermissionExplanation } from "./types";

describe("permissionExplanation", () => {
  it("extracts a well-formed explanation from a 403's details", () => {
    const exp: PermissionExplanation = {
      capability: "netWrite",
      granted: false,
      missing: [{ privilege: "Sys.Modify", path: "/nodes/pve1", confirmed: true }],
    };
    const err = new ApiError(403, "forbidden", "missing required capability: netWrite", { explanation: exp });
    expect(permissionExplanation(err)).toEqual(exp);
  });

  it("returns undefined for a 403 with no explanation in details", () => {
    const err = new ApiError(403, "forbidden", "refused");
    expect(permissionExplanation(err)).toBeUndefined();
  });

  it("returns undefined for a non-403 ApiError even if details.explanation is present", () => {
    // A 401/404/5xx never carries this shape in practice, but the guard
    // must not be fooled by a details bag that happens to look right.
    const err = new ApiError(500, "internal_error", "boom", {
      explanation: { capability: "netWrite", granted: false },
    });
    expect(permissionExplanation(err)).toBeUndefined();
  });

  it("returns undefined for a malformed explanation payload", () => {
    const err = new ApiError(403, "forbidden", "refused", { explanation: { capability: "netWrite" } });
    expect(permissionExplanation(err)).toBeUndefined();
  });

  it("returns undefined for a plain (non-ApiError) error", () => {
    expect(permissionExplanation(new Error("network down"))).toBeUndefined();
  });
});

describe("explainPermission", () => {
  it("lists a single missing privilege", () => {
    const text = explainPermission({
      capability: "netWrite",
      granted: false,
      missing: [{ privilege: "Sys.Modify", path: "/nodes/pve1", confirmed: true }],
    });
    expect(text).toBe("Missing PVE privilege: Sys.Modify at /nodes/pve1.");
  });

  it("lists every missing privilege, not just the first, pluralized", () => {
    const text = explainPermission({
      capability: "capture",
      granted: false,
      missing: [
        { privilege: "Sys.Modify", path: "/nodes/pve1", confirmed: true },
        { privilege: "Sys.Console", path: "/nodes/pve1", confirmed: false },
      ],
    });
    expect(text).toBe(
      "Missing PVE privileges: Sys.Modify at /nodes/pve1, Sys.Console at /nodes/pve1 (also required; not confirmed missing).",
    );
  });

  it("prefers reason over missing when both are somehow present", () => {
    const text = explainPermission({
      capability: "netWrite",
      granted: false,
      reason: "this daemon is running with read_only = true",
      missing: [{ privilege: "Sys.Modify", path: "/", confirmed: true }],
    });
    expect(text).toBe("this daemon is running with read_only = true");
  });

  it("returns undefined for a granted capability", () => {
    expect(explainPermission({ capability: "netRead", granted: true })).toBeUndefined();
  });
});
