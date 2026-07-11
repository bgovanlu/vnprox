import { describe, expect, it } from "vitest";
import type { BlueprintParamDef } from "../api/types";
import { defaultRawValue, validateParamForm, validateParamInput } from "./paramValidation";

describe("validateParamInput", () => {
  it("rejects a bad CIDR (T-603 AC4)", () => {
    const def: BlueprintParamDef = { name: "addr", type: "cidr", required: true };
    expect(validateParamInput(def, "192.168.1.10").valid).toBe(false);
    expect(validateParamInput(def, "not-an-address").valid).toBe(false);
    expect(validateParamInput(def, "999.1.1.1/24").valid).toBe(false);
  });

  it("accepts a valid CIDR", () => {
    const def: BlueprintParamDef = { name: "addr", type: "cidr", required: true };
    const result = validateParamInput(def, "192.168.1.10/24");
    expect(result.valid).toBe(true);
    expect(result.value).toBe("192.168.1.10/24");
  });

  it("rejects an out-of-range VID (T-603 AC4)", () => {
    const def: BlueprintParamDef = { name: "vid", type: "vid", required: true };
    expect(validateParamInput(def, "0").valid).toBe(false);
    expect(validateParamInput(def, "4095").valid).toBe(false);
    expect(validateParamInput(def, "not-a-number").valid).toBe(false);
  });

  it("accepts an in-range VID", () => {
    const def: BlueprintParamDef = { name: "vid", type: "vid", required: true };
    const result = validateParamInput(def, "20");
    expect(result.valid).toBe(true);
    expect(result.value).toBe(20);
  });

  it("parses a vidList, rejecting any out-of-range member", () => {
    const def: BlueprintParamDef = { name: "vlans", type: "vidList", required: true };
    const ok = validateParamInput(def, "10, 20, 30");
    expect(ok.valid).toBe(true);
    expect(ok.value).toEqual([10, 20, 30]);

    const bad = validateParamInput(def, "10, 5000");
    expect(bad.valid).toBe(false);
  });

  it("rejects a bare IP for a cidr param and a CIDR for an ip param", () => {
    const cidrDef: BlueprintParamDef = { name: "addr", type: "cidr" };
    expect(validateParamInput(cidrDef, "10.0.0.1").valid).toBe(false);

    const ipDef: BlueprintParamDef = { name: "gw", type: "ip" };
    expect(validateParamInput(ipDef, "10.0.0.1/24").valid).toBe(false);
    expect(validateParamInput(ipDef, "10.0.0.1").valid).toBe(true);
  });

  it("treats an empty optional field as valid, an empty required field as an error", () => {
    const optional: BlueprintParamDef = { name: "x", type: "string", required: false };
    expect(validateParamInput(optional, "").valid).toBe(true);

    const required: BlueprintParamDef = { name: "x", type: "string", required: true, label: "X" };
    const result = validateParamInput(required, "");
    expect(result.valid).toBe(false);
    expect(result.error).toContain("required");
  });

  it("parses bool and int types", () => {
    expect(validateParamInput({ name: "b", type: "bool" }, "true")).toEqual({ valid: true, value: true });
    expect(validateParamInput({ name: "b", type: "bool" }, "maybe").valid).toBe(false);
    expect(validateParamInput({ name: "n", type: "int" }, "42")).toEqual({ valid: true, value: 42 });
    expect(validateParamInput({ name: "n", type: "int" }, "4.2").valid).toBe(false);
  });
});

describe("validateParamForm", () => {
  const defs: BlueprintParamDef[] = [
    { name: "bridgeName", type: "string", required: true, default: "vmbr0" },
    { name: "mgmtCidr", type: "cidr", required: true, default: "192.168.1.10/24" },
    { name: "guestVlans", type: "vidList", required: true, default: [10, 20, 30] },
  ];

  it("returns parsed params when every field is valid", () => {
    const { errors, params } = validateParamForm(defs, {
      bridgeName: "vmbr0",
      mgmtCidr: "192.168.1.20/24",
      guestVlans: "10, 20",
    });
    expect(errors).toEqual({});
    expect(params).toEqual({ bridgeName: "vmbr0", mgmtCidr: "192.168.1.20/24", guestVlans: [10, 20] });
  });

  it("collects one error per invalid field and omits params", () => {
    const { errors, params } = validateParamForm(defs, {
      bridgeName: "vmbr0",
      mgmtCidr: "not-a-cidr",
      guestVlans: "9999",
    });
    expect(params).toBeUndefined();
    expect(Object.keys(errors).sort()).toEqual(["guestVlans", "mgmtCidr"]);
  });
});

describe("defaultRawValue", () => {
  it("renders a scalar default as a string", () => {
    expect(defaultRawValue({ name: "x", type: "string", default: "vmbr0" })).toBe("vmbr0");
    expect(defaultRawValue({ name: "x", type: "vid", default: 20 })).toBe("20");
  });

  it("renders an array default joined with commas", () => {
    expect(defaultRawValue({ name: "x", type: "vidList", default: [10, 20, 30] })).toBe("10, 20, 30");
  });

  it("renders no default as an empty string", () => {
    expect(defaultRawValue({ name: "x", type: "string" })).toBe("");
  });
});
