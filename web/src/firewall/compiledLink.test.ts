// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { compiledChainDeepLinkPath, ruleEditorDeepLinkPath } from "./compiledLink";

describe("compiledChainDeepLinkPath", () => {
  it("builds a cluster-scope link with no node param", () => {
    expect(compiledChainDeepLinkPath("cluster", 3)).toBe("/firewall/compiled?scope=cluster&pos=3");
  });

  it("builds a node-scope link carrying the node param", () => {
    expect(compiledChainDeepLinkPath("node", 0, "pve1")).toBe("/firewall/compiled?scope=node&pos=0&node=pve1");
  });
});

describe("ruleEditorDeepLinkPath", () => {
  it("builds a cluster-scope link back to the firewall page", () => {
    expect(ruleEditorDeepLinkPath("cluster", 2)).toBe("/firewall?scope=cluster&pos=2");
  });

  it("builds a node-scope link carrying the node param", () => {
    expect(ruleEditorDeepLinkPath("node", 5, "pve2")).toBe("/firewall?scope=node&pos=5&node=pve2");
  });
});
