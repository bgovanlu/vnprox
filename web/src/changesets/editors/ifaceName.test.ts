import { describe, expect, it } from "vitest";
import { ifaceNameError } from "./ifaceName";

describe("ifaceNameError", () => {
  it("accepts valid interface names", () => {
    expect(ifaceNameError("vmbrmgmt")).toBeUndefined();
    expect(ifaceNameError("vmbr0.100")).toBeUndefined();
    expect(ifaceNameError("bond0")).toBeUndefined();
  });

  it("rejects empty, unchanged, over-long, and illegal-character names", () => {
    expect(ifaceNameError("")).toBeDefined();
    expect(ifaceNameError("  ")).toBeDefined();
    expect(ifaceNameError("vmbr0", "vmbr0")).toBeDefined(); // no-op
    expect(ifaceNameError("thisnameiswaytoolong")).toBeDefined(); // > 15 chars
    expect(ifaceNameError("has space")).toBeDefined();
    expect(ifaceNameError("has/slash")).toBeDefined();
    expect(ifaceNameError(".leadingdot")).toBeDefined();
  });
});
