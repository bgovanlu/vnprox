// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { GuestNicRow } from "./guestNics";
import { macPickerOptions } from "./macPicker";

function row(overrides: Partial<GuestNicRow>): GuestNicRow {
  return { ref: "guest-nic:pve1:100/net0", label: "web1/net0", node: "pve1", linkDown: false, ...overrides };
}

describe("macPickerOptions", () => {
  it("skips rows with no known mac", () => {
    const rows = [row({ mac: undefined }), row({ ref: "guest-nic:pve1:101/net0", label: "web2/net0", mac: "AA:BB:CC:DD:EE:02" })];
    const opts = macPickerOptions(rows);
    expect(opts).toHaveLength(1);
    expect(opts[0]?.mac).toBe("AA:BB:CC:DD:EE:02");
  });

  it("dedupes by mac", () => {
    const rows = [
      row({ mac: "AA:BB:CC:DD:EE:01" }),
      row({ ref: "guest-nic:pve1:100/net1", label: "web1/net1", mac: "AA:BB:CC:DD:EE:01" }),
    ];
    expect(macPickerOptions(rows)).toHaveLength(1);
  });

  it("keeps guestLabel undecorated and optionLabel as 'label (mac)'", () => {
    const rows = [row({ label: "web1/net0", mac: "AA:BB:CC:DD:EE:01" })];
    const [opt] = macPickerOptions(rows);
    expect(opt?.guestLabel).toBe("web1/net0");
    expect(opt?.optionLabel).toBe("web1/net0 (AA:BB:CC:DD:EE:01)");
  });

  it("sorts by optionLabel", () => {
    const rows = [
      row({ label: "zebra/net0", mac: "AA:BB:CC:DD:EE:99" }),
      row({ ref: "guest-nic:pve1:1/net0", label: "alpha/net0", mac: "AA:BB:CC:DD:EE:01" }),
    ];
    const opts = macPickerOptions(rows);
    expect(opts.map((o) => o.guestLabel)).toEqual(["alpha/net0", "zebra/net0"]);
  });

  it("empty input yields no options", () => {
    expect(macPickerOptions([])).toEqual([]);
  });
});
