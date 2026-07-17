import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { FlowEdge } from "./flowEdges";
import { FlowPairPanel } from "./FlowPairPanel";

const edge: FlowEdge = {
  id: "bridge:pve1:vmbr0=>sdn-vnet::z/v100::flow",
  from: "bridge:pve1:vmbr0",
  to: "sdn-vnet::z/v100",
  bytes: 3000,
  packets: 30,
  recordCount: 2,
  lastAt: 1000,
  bytesPerSec: 50,
};

describe("FlowPairPanel", () => {
  it("renders the conversation summary and endpoints", () => {
    render(
      <MemoryRouter>
        <FlowPairPanel edge={edge} onClose={() => undefined} />
      </MemoryRouter>,
    );
    expect(screen.getByText("bridge:pve1:vmbr0")).toBeInTheDocument();
    expect(screen.getByText("sdn-vnet::z/v100")).toBeInTheDocument();
    expect(screen.getByText("30")).toBeInTheDocument();
  });

  it("the 'view in Flow Explorer' link carries the pair as URL state", () => {
    render(
      <MemoryRouter>
        <FlowPairPanel edge={edge} onClose={() => undefined} />
      </MemoryRouter>,
    );
    const link = screen.getByRole("link", { name: "View in Flow Explorer" });
    const href = link.getAttribute("href") ?? "";
    expect(href).toContain("/flows?");
    expect(href).toContain(encodeURIComponent("bridge:pve1:vmbr0"));
    expect(href).toContain("pairDst=");
  });

  it("calls onClose when the close button is clicked", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <MemoryRouter>
        <FlowPairPanel edge={edge} onClose={onClose} />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledOnce();
  });
});
