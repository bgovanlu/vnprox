import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ObjectsPanel } from "./ObjectsPanel";
import type { FirewallObjectsResponse } from "../api/types";

const objects: FirewallObjectsResponse = {
  aliases: [
    {
      kind: "alias", scope: "cluster", name: "office_net", comment: "office/management subnet", count: 9,
      referencedBy: [
        { scope: "cluster", ref: "fw-ruleset::cluster", pos: 0 },
        { scope: "guest", ref: "fw-ruleset:pve1:guest/qemu/100", pos: 2 },
      ],
    },
    { kind: "alias", scope: "guest", name: "unused_alias", count: 0 },
  ],
  ipsets: [
    { kind: "ipset", scope: "cluster", name: "blocklist", count: 1, referencedBy: [{ scope: "cluster", ref: "fw-ruleset::cluster", pos: 1 }] },
  ],
  groups: [
    { kind: "group", scope: "cluster", name: "webservers", count: 2 },
  ],
  macros: [
    { name: "HTTP", comment: "Web traffic (HTTP)", ports: [{ proto: "tcp", dport: "80" }] },
  ],
};

describe("ObjectsPanel", () => {
  it("shows the referenced-by-N-rules count for each object", () => {
    render(<ObjectsPanel objects={objects} />);
    expect(screen.getByText("referenced by 9 rules — view")).toBeInTheDocument();
    expect(screen.getByText("referenced by 1 rule — view")).toBeInTheDocument();
    expect(screen.getByText("referenced by 2 rules — view")).toBeInTheDocument();
  });

  it("labels a zero-reference object honestly instead of a misleading count", () => {
    render(<ObjectsPanel objects={objects} />);
    expect(screen.getByText("not referenced")).toBeInTheDocument();
  });

  it("expands the reference list on click (the 'view' deep-link)", async () => {
    const user = userEvent.setup();
    render(<ObjectsPanel objects={objects} />);
    await user.click(screen.getByText("referenced by 9 rules — view"));
    expect(screen.getByText(/fw-ruleset::cluster/)).toBeInTheDocument();
    expect(screen.getByText(/fw-ruleset:pve1:guest\/qemu\/100/)).toBeInTheDocument();
  });

  it("renders the macro catalog with its expansion preview", () => {
    render(<ObjectsPanel objects={objects} />);
    expect(screen.getByText("HTTP → tcp/80")).toBeInTheDocument();
  });
});
