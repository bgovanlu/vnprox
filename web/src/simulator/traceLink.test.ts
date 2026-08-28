// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { isTraceableEntityKind, traceFromPath, traceToExternalPath, traceToPath } from "./traceLink";

describe("isTraceableEntityKind", () => {
  it("only guest-nic entities are traceable", () => {
    expect(isTraceableEntityKind("guest-nic")).toBe(true);
    expect(isTraceableEntityKind("guest")).toBe(false);
    expect(isTraceableEntityKind("bridge")).toBe(false);
  });
});

describe("traceFromPath", () => {
  it("pre-fills the source only, for a guest-nic entity", () => {
    const path = traceFromPath("guest-nic", "guest-nic:pve1:100/net0");
    expect(path).toBe("/tools?srcKind=guest-nic&srcRef=guest-nic%3Apve1%3A100%2Fnet0");
  });

  it("returns undefined for a non-traceable entity kind", () => {
    expect(traceFromPath("bridge", "bridge:pve1:vmbr0")).toBeUndefined();
  });
});

describe("traceToPath", () => {
  it("pre-fills the destination only", () => {
    const path = traceToPath("guest-nic", "guest-nic:pve2:200/net0");
    expect(path).toBe("/tools?dstKind=guest-nic&dstRef=guest-nic%3Apve2%3A200%2Fnet0");
  });
});

describe("traceToExternalPath", () => {
  it("pre-fills both source (this entity) and destination (external)", () => {
    const path = traceToExternalPath("guest-nic", "guest-nic:pve1:100/net0");
    expect(path).toBe("/tools?srcKind=guest-nic&srcRef=guest-nic%3Apve1%3A100%2Fnet0&dstKind=external");
  });

  it("returns undefined for a non-traceable entity kind", () => {
    expect(traceToExternalPath("bond", "bond:pve1:bond0")).toBeUndefined();
  });
});
