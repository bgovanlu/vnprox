// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyNode } from "../api/types";
import { describeIpSubnetContext, subnetsContaining } from "./ipSubnetContext";

function subnetNode(cidr: string): TopologyNode {
  return { id: `sdn-subnet::zone1/vnet1/${cidr}`, kind: "sdn-subnet", label: cidr, layer: "sdn", nodeGroup: "", status: "ok", badges: [] };
}

describe("subnetsContaining", () => {
  const nodes = [subnetNode("10.100.0.0/24"), subnetNode("10.100.0.0/28"), subnetNode("10.200.0.0/24")];

  it("finds every containing subnet, most-specific first", () => {
    expect(subnetsContaining("10.100.0.5", nodes)).toEqual(["10.100.0.0/28", "10.100.0.0/24"]);
  });

  it("returns an empty list for an address in no known subnet", () => {
    expect(subnetsContaining("192.168.99.1", nodes)).toEqual([]);
  });

  it("respects prefix boundaries (not a naive string-prefix match)", () => {
    // 10.100.0.20 is outside the /28 (10.100.0.0-15) but inside the /24.
    expect(subnetsContaining("10.100.0.20", nodes)).toEqual(["10.100.0.0/24"]);
  });

  it("returns an empty list for an unparseable address", () => {
    expect(subnetsContaining("not-an-ip", nodes)).toEqual([]);
  });

  it("a /0 subnet contains everything", () => {
    expect(subnetsContaining("8.8.8.8", [subnetNode("0.0.0.0/0")])).toEqual(["0.0.0.0/0"]);
  });

  it("ignores non-subnet topology nodes", () => {
    const bridgeNode: TopologyNode = { id: "bridge:pve1:vmbr0", kind: "bridge", label: "10.100.0.0/24", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] };
    expect(subnetsContaining("10.100.0.5", [bridgeNode])).toEqual([]);
  });
});

describe("describeIpSubnetContext", () => {
  const nodes = [subnetNode("10.100.0.0/24")];

  it("names the containing subnet", () => {
    expect(describeIpSubnetContext("10.100.0.5", nodes)).toBe("Within 10.100.0.0/24.");
  });

  it("is honest when no subnet is known", () => {
    expect(describeIpSubnetContext("203.0.113.1", nodes)).toBe("No known SDN subnet contains this address.");
  });

  it("is honest when the input isn't a recognizable address yet", () => {
    expect(describeIpSubnetContext("10.100", nodes)).toBe("Not a recognized IPv4 address.");
  });
});
