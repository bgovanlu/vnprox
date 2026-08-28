// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { MacroView } from "../api/types";
import { macroToPreset, servicePresetsFromMacros } from "./servicePresets";

describe("macroToPreset", () => {
  it("converts a single-port macro directly", () => {
    const macro: MacroView = { name: "HTTP", comment: "Web traffic (HTTP)", ports: [{ proto: "tcp", dport: "80" }] };
    expect(macroToPreset(macro)).toEqual({ name: "HTTP", comment: "Web traffic (HTTP)", proto: "tcp", port: 80 });
  });

  it("uses the first pair of a multi-port macro (DNS: udp+tcp/53)", () => {
    const macro: MacroView = {
      name: "DNS",
      ports: [
        { proto: "udp", dport: "53" },
        { proto: "tcp", dport: "53" },
      ],
    };
    expect(macroToPreset(macro)).toEqual({ name: "DNS", comment: undefined, proto: "udp", port: 53 });
  });

  it("leaves port undefined for a port-less macro (ICMP/Ping)", () => {
    const macro: MacroView = { name: "Ping", ports: [{ proto: "icmp" }] };
    expect(macroToPreset(macro)).toEqual({ name: "Ping", comment: undefined, proto: "icmp", port: undefined });
  });

  it("leaves port undefined for a range/list dport rather than guessing", () => {
    const macro: MacroView = { name: "VNC", ports: [{ proto: "tcp", dport: "5900:5999" }] };
    expect(macroToPreset(macro).port).toBeUndefined();
    const macro2: MacroView = { name: "SMTPS-ish", ports: [{ proto: "tcp", dport: "465,587" }] };
    expect(macroToPreset(macro2).port).toBeUndefined();
  });

  it("handles a macro with no ports at all", () => {
    const macro: MacroView = { name: "Empty", ports: [] };
    expect(macroToPreset(macro)).toEqual({ name: "Empty", comment: undefined, proto: undefined, port: undefined });
  });
});

describe("servicePresetsFromMacros", () => {
  it("maps every macro in order", () => {
    const macros: MacroView[] = [
      { name: "HTTP", ports: [{ proto: "tcp", dport: "80" }] },
      { name: "HTTPS", ports: [{ proto: "tcp", dport: "443" }] },
    ];
    const presets = servicePresetsFromMacros(macros);
    expect(presets.map((p) => p.name)).toEqual(["HTTP", "HTTPS"]);
  });

  it("returns an empty list for no macros", () => {
    expect(servicePresetsFromMacros([])).toEqual([]);
  });
});
