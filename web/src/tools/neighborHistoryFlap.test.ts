// SPDX-License-Identifier: Apache-2.0

// Table-driven-style coverage for the neighbor binding timeline's pure
// grouping/flap-highlighting logic (T-3905), mirroring the Go backend's
// own IP_FLAP_THRESHOLD/MAC_CLAIM_THRESHOLD boundary tests
// (internal/neighbor/history_test.go).
import { describe, expect, it } from "vitest";
import type { NeighborBinding } from "../api/types";
import {
  IP_FLAP_THRESHOLD,
  IP_FLAP_WINDOW_SECONDS,
  MAC_CLAIM_THRESHOLD,
  MAC_CLAIM_WINDOW_SECONDS,
  findMacClaims,
  groupNeighborHistory,
  isIPChurnFlapping,
} from "./neighborHistoryFlap";

function binding(overrides: Partial<NeighborBinding> & Pick<NeighborBinding, "at">): NeighborBinding {
  return {
    node: "pve1",
    ip: "10.0.0.1",
    mac: "aa:aa:aa:aa:aa:01",
    firstSeen: false,
    ...overrides,
  };
}

describe("groupNeighborHistory", () => {
  it("groups by (node, ip), newest event first within a group, newest group first overall", () => {
    const items: NeighborBinding[] = [
      binding({ at: 100, ip: "10.0.0.1", mac: "aa:aa:aa:aa:aa:01", firstSeen: true }),
      binding({ at: 200, ip: "10.0.0.1", mac: "aa:aa:aa:aa:aa:02", prevMac: "aa:aa:aa:aa:aa:01" }),
      binding({ at: 150, node: "pve2", ip: "10.0.0.2", mac: "bb:bb:bb:bb:bb:01", firstSeen: true }),
    ];
    const groups = groupNeighborHistory(items);
    expect(groups).toHaveLength(2);
    // The pve1/10.0.0.1 group's newest event (200) is newer than
    // pve2/10.0.0.2's only event (150), so it sorts first.
    const [first, second] = groups;
    expect(first?.node).toBe("pve1");
    expect(first?.ip).toBe("10.0.0.1");
    expect(first?.events.map((e) => e.at)).toEqual([200, 100]);
    expect(second?.node).toBe("pve2");
  });

  it("keeps the same IP on two different nodes as two separate groups", () => {
    const items: NeighborBinding[] = [
      binding({ at: 100, node: "pve1", ip: "10.0.0.1", firstSeen: true }),
      binding({ at: 100, node: "pve2", ip: "10.0.0.1", firstSeen: true }),
    ];
    const groups = groupNeighborHistory(items);
    expect(groups).toHaveLength(2);
  });

  it("returns an empty list for an empty page", () => {
    expect(groupNeighborHistory([])).toEqual([]);
  });
});

describe("isIPChurnFlapping (IP_FLAP_THRESHOLD boundary)", () => {
  // Mirrors internal/neighbor's TestHistoryRecorder_Flaps_IPChurn_ThresholdBoundary.
  const cases: { name: string; transitions: number; want: boolean }[] = [
    { name: "zero transitions: stable binding, no flap", transitions: 0, want: false },
    { name: "one transition: a clean single rebind, not a flap", transitions: 1, want: false },
    { name: "just under threshold", transitions: IP_FLAP_THRESHOLD - 1, want: false },
    { name: "exactly at threshold", transitions: IP_FLAP_THRESHOLD, want: true },
    { name: "over threshold", transitions: IP_FLAP_THRESHOLD + 2, want: true },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      const newestAt = 10_000;
      // First row (first-seen, no prevMac) plus tc.transitions genuine
      // rebinds, all within the window (spaced 1s apart, most recent at
      // newestAt).
      const events: NeighborBinding[] = [];
      for (let i = 0; i <= tc.transitions; i++) {
        const at = newestAt - (tc.transitions - i);
        events.push(
          i === 0
            ? binding({ at, mac: "aa:aa:aa:aa:aa:00", firstSeen: true })
            : binding({ at, mac: `aa:aa:aa:aa:aa:0${String(i)}`, prevMac: `aa:aa:aa:aa:aa:0${String(i - 1)}` }),
        );
      }
      const newestFirst = [...events].sort((a, b) => b.at - a.at);
      expect(isIPChurnFlapping(newestFirst)).toBe(tc.want);
    });
  }

  it("does not count transitions outside the trailing window", () => {
    const newestAt = 10_000;
    const events: NeighborBinding[] = [
      binding({ at: newestAt, mac: "aa:aa:aa:aa:aa:03", prevMac: "aa:aa:aa:aa:aa:02" }),
      // These two genuine transitions are well before the window
      // (IP_FLAP_WINDOW_SECONDS before newestAt), so despite there being
      // IP_FLAP_THRESHOLD total transitions, only one is in-window.
      binding({ at: newestAt - IP_FLAP_WINDOW_SECONDS * 3, mac: "aa:aa:aa:aa:aa:02", prevMac: "aa:aa:aa:aa:aa:01" }),
      binding({ at: newestAt - IP_FLAP_WINDOW_SECONDS * 4, mac: "aa:aa:aa:aa:aa:01", prevMac: "aa:aa:aa:aa:aa:00" }),
    ];
    expect(isIPChurnFlapping(events)).toBe(false);
  });

  it("returns false for an empty event list", () => {
    expect(isIPChurnFlapping([])).toBe(false);
  });
});

describe("findMacClaims (MAC_CLAIM_THRESHOLD boundary)", () => {
  const cases: { name: string; ips: number; want: boolean }[] = [
    { name: "one IP: an ordinary single binding", ips: 1, want: false },
    { name: "just under threshold", ips: MAC_CLAIM_THRESHOLD - 1, want: false },
    { name: "exactly at threshold", ips: MAC_CLAIM_THRESHOLD, want: true },
    { name: "over threshold", ips: MAC_CLAIM_THRESHOLD + 3, want: true },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      const newestAt = 10_000;
      const mac = "bb:bb:bb:bb:bb:01";
      const items: NeighborBinding[] = Array.from({ length: tc.ips }, (_, i) =>
        binding({ at: newestAt, ip: `10.0.0.${String(i + 1)}`, mac, firstSeen: true }),
      );
      const claims = findMacClaims(items);
      const claim = claims.find((c) => c.mac === mac);
      expect(claim !== undefined).toBe(tc.want);
      if (claim) {
        expect(claim.ips).toHaveLength(tc.ips);
      }
    });
  }

  it("excludes IPs claimed outside the trailing window", () => {
    const newestAt = 10_000;
    const mac = "bb:bb:bb:bb:bb:01";
    const items: NeighborBinding[] = [
      ...Array.from({ length: MAC_CLAIM_THRESHOLD - 1 }, (_, i) =>
        binding({ at: newestAt, ip: `10.0.0.${String(i + 1)}`, mac, firstSeen: true }),
      ),
      // One more claimed IP, but well outside MAC_CLAIM_WINDOW_SECONDS —
      // must not push this MAC over threshold.
      binding({ at: newestAt - MAC_CLAIM_WINDOW_SECONDS * 5, ip: "10.0.0.99", mac, firstSeen: true }),
    ];
    expect(findMacClaims(items).some((c) => c.mac === mac)).toBe(false);
  });

  it("returns an empty list for an empty page", () => {
    expect(findMacClaims([])).toEqual([]);
  });
});
