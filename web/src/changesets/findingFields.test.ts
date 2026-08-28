// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { Finding } from "../api/types";
import { editorFindingsFor, fieldForFindingCode, hasEditorErrors } from "./findingFields";

describe("fieldForFindingCode", () => {
  it("maps field-attributable codes to their editor field", () => {
    expect(fieldForFindingCode("schema.mtu_out_of_range")).toBe("mtu");
    expect(fieldForFindingCode("schema.duplicate_slave")).toBe("slaves");
    expect(fieldForFindingCode("referential.parent_not_found")).toBe("parent");
    expect(fieldForFindingCode("referential.vid_overlap")).toBe("vids");
  });

  it("returns undefined for codes with no single originating field", () => {
    expect(fieldForFindingCode("referential.already_exists")).toBeUndefined();
    expect(fieldForFindingCode("safety.protected_interface")).toBeUndefined();
    expect(fieldForFindingCode("not.a.real.code")).toBeUndefined();
  });
});

describe("editorFindingsFor", () => {
  const target = "bridge:pve1:vmbr1";
  const findings: Finding[] = [
    { severity: "error", code: "schema.mtu_out_of_range", message: "mtu 99999 out of range", ref: target },
    { severity: "error", code: "referential.already_exists", message: "vmbr1 already exists", ref: target },
    { severity: "warning", code: "advisory.bridge_missing_comment", message: "no comment", ref: target },
    { severity: "error", code: "schema.cidr_invalid", message: "other entity's error", ref: "bridge:pve2:vmbr9" },
  ];

  it("splits error findings for the target into per-field and general buckets", () => {
    const split = editorFindingsFor(findings, target);
    expect(split.byField).toEqual({ mtu: ["mtu 99999 out of range"] });
    expect(split.general).toEqual(["vmbr1 already exists"]);
  });

  it("excludes warnings and other targets' findings", () => {
    const split = editorFindingsFor(findings, target);
    const all = [...split.general, ...Object.values(split.byField).flat()];
    expect(all).not.toContain("no comment");
    expect(all).not.toContain("other entity's error");
  });

  it("hasEditorErrors is false for a clean/warning-only split", () => {
    expect(hasEditorErrors(editorFindingsFor([], target))).toBe(false);
    expect(
      hasEditorErrors(
        editorFindingsFor(
          [{ severity: "warning", code: "advisory.bond_single_slave", message: "one slave", ref: target }],
          target,
        ),
      ),
    ).toBe(false);
    expect(hasEditorErrors(editorFindingsFor(findings, target))).toBe(true);
  });
});
