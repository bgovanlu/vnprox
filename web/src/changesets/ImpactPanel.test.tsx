// SPDX-License-Identifier: Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ImpactPanel } from "./ImpactPanel";
import type { ChangesetImpact } from "../api/types";

const outage: ChangesetImpact = {
  nodes: ["pve1"],
  carriers: ["bridge:pve1:vmbr0"],
  guests: [
    { ref: "guest:pve1:100", name: "app01", node: "pve1", vmid: 100, nic: "net0", carrier: "bridge:pve1:vmbr0" },
    { ref: "guest:pve1:101", name: "db01", node: "pve1", vmid: 101, nic: "net0", carrier: "bridge:pve1:vmbr0" },
  ],
  ops: [
    {
      op: "bridge.delete",
      target: "bridge:pve1:vmbr0",
      disruption: "outage",
      reason: "2 guests attached to bridge:pve1:vmbr0 lose their path",
    },
  ],
  disruption: "outage",
  touchesMgmtPath: true,
};

describe("ImpactPanel", () => {
  it("leads with the verdict, the counts, and the management-path warning", () => {
    render(<ImpactPanel impact={outage} />);
    expect(screen.getByText("Outage")).toBeInTheDocument();
    expect(screen.getByText(/1 node \(pve1\)/)).toBeInTheDocument();
    expect(screen.getByText(/2 guests affected/)).toBeInTheDocument();
    expect(screen.getByText(/touches a management path/)).toBeInTheDocument();
  });

  it("names each affected guest, not just a count", () => {
    render(<ImpactPanel impact={outage} />);
    expect(screen.getByText("app01")).toBeInTheDocument();
    expect(screen.getByText("db01")).toBeInTheDocument();
  });

  // The central rule: the panel renders the SERVER's reason verbatim. A UI
  // that synthesised its own wording could drift from what the server actually
  // decided, and then the warning would be describing something else.
  it("renders the server's reason for every operation", () => {
    render(<ImpactPanel impact={outage} />);
    expect(screen.getByText("2 guests attached to bridge:pve1:vmbr0 lose their path")).toBeInTheDocument();
  });

  it("distinguishes 'could not compute' from 'no impact'", () => {
    render(<ImpactPanel error />);
    const msg = screen.getByText(/Could not compute the impact/);
    expect(msg).toBeInTheDocument();
    // This is the load-bearing half: an operator must not read a failure as
    // an all-clear.
    expect(msg.textContent).toMatch(/Do not read this as/);
  });

  it("shows a loading state rather than an empty all-clear", () => {
    render(<ImpactPanel loading />);
    expect(screen.getByText(/Computing impact/)).toBeInTheDocument();
    expect(screen.queryByText("No disruption")).not.toBeInTheDocument();
  });

  it("renders an empty changeset as no disruption with no guest section", () => {
    const empty: ChangesetImpact = {
      nodes: [], carriers: [], guests: [], ops: [], disruption: "none", touchesMgmtPath: false,
    };
    render(<ImpactPanel impact={empty} />);
    expect(screen.getByText("No disruption")).toBeInTheDocument();
    expect(screen.queryByText("Guests affected")).not.toBeInTheDocument();
    expect(screen.getByText("No operations.")).toBeInTheDocument();
    expect(screen.queryByText(/touches a management path/)).not.toBeInTheDocument();
  });

  it("uses singular wording for one node and one guest", () => {
    const one: ChangesetImpact = {
      nodes: ["pve1"],
      carriers: ["bridge:pve1:vmbr0"],
      guests: [{ ref: "guest:pve1:100", name: "app01", node: "pve1", vmid: 100, nic: "net0", carrier: "bridge:pve1:vmbr0" }],
      ops: [{ op: "bridge.update", target: "bridge:pve1:vmbr0", disruption: "brief", reason: "reload re-creates it" }],
      disruption: "brief",
      touchesMgmtPath: false,
    };
    render(<ImpactPanel impact={one} />);
    expect(screen.getByText(/1 guest affected/)).toBeInTheDocument();
    expect(screen.queryByText(/1 guests affected/)).not.toBeInTheDocument();
  });
});
