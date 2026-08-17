import { describe, expect, it } from "vitest";
import { scopeToggleSummary } from "./scopeSummary";

// Acceptance criterion 5: scope-disable summary correctly states
// consequences for each scope — golden strings, word-for-word.
describe("scopeToggleSummary", () => {
  it("cluster OFF: states the full cascade to every node and guest", () => {
    expect(scopeToggleSummary({ scope: "cluster", enabling: false })).toBe(
      "Turning the datacenter firewall OFF deactivates every rule in the cluster ruleset. Because cluster rules apply directly to every guest's evaluation order, every node's and every guest's firewall becomes unenforced too — there is no way to keep an individual node or guest protected while the datacenter firewall is off.",
    );
  });

  it("cluster ON: warns about immediate cluster-wide activation", () => {
    expect(scopeToggleSummary({ scope: "cluster", enabling: true })).toBe(
      "Turning the datacenter firewall ON activates every cluster-wide rule immediately, for every node and every guest — including any rule that could block management access. Review the ruleset before confirming.",
    );
  });

  it("node OFF: scoped to that node only, names it", () => {
    expect(scopeToggleSummary({ scope: "node", enabling: false, node: "pve1" })).toBe(
      "Turning the firewall OFF for node pve1 deactivates only that node's own host-level rules; the datacenter firewall and every guest's firewall are unaffected.",
    );
  });

  it("node ON: scoped to that node only, names it", () => {
    expect(scopeToggleSummary({ scope: "node", enabling: true, node: "pve1" })).toBe(
      "Turning the firewall ON for node pve1 activates that node's own host-level rules. The datacenter firewall and every guest's firewall are unaffected by this change.",
    );
  });

  it("node with no name falls back to a generic label", () => {
    expect(scopeToggleSummary({ scope: "node", enabling: false })).toContain("for node this node");
  });

  it("guest OFF: scoped to that guest only, names it", () => {
    expect(scopeToggleSummary({ scope: "guest", enabling: false, guestLabel: "web01" })).toBe(
      "Turning the firewall OFF for web01 deactivates its own rules and any security groups it includes; the datacenter firewall and node firewall are unaffected.",
    );
  });

  it("guest ON: scoped to that guest only, names it", () => {
    expect(scopeToggleSummary({ scope: "guest", enabling: true, guestLabel: "web01" })).toBe(
      "Turning the firewall ON for web01 activates its own rules and any security groups it includes. The datacenter firewall and node firewall are unaffected by this change.",
    );
  });

  it("vnet OFF: scoped to that vnet's forward chain only, names it", () => {
    expect(scopeToggleSummary({ scope: "vnet", enabling: false, vnetLabel: "vnet100" })).toBe(
      "Turning the firewall OFF for vnet100 deactivates its own forward-chain rules; the datacenter firewall and every node's and guest's firewall are unaffected.",
    );
  });

  it("vnet ON: scoped to that vnet's forward chain only, names it", () => {
    expect(scopeToggleSummary({ scope: "vnet", enabling: true, vnetLabel: "vnet100" })).toBe(
      "Turning the firewall ON for vnet100 activates its own forward-chain rules. The datacenter firewall and every node's and guest's firewall are unaffected by this change.",
    );
  });

  it("vnet with no name falls back to a generic label", () => {
    expect(scopeToggleSummary({ scope: "vnet", enabling: false })).toContain("for this vnet");
  });
});
