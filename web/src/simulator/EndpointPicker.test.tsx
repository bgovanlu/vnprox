// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SearchResponse, SimEndpointSpec, TopologyNode } from "../api/types";
import { EndpointPicker } from "./EndpointPicker";

const searchInventoryMock = vi.fn<(q: string) => Promise<SearchResponse>>();

vi.mock("../api/topology", () => ({
  searchInventory: (q: string) => searchInventoryMock(q),
}));

function renderPicker(props: {
  value: SimEndpointSpec | undefined;
  onChange: (spec: SimEndpointSpec | undefined) => void;
  topologyNodes?: TopologyNode[];
}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <EndpointPicker label="Source" value={props.value} onChange={props.onChange} topologyNodes={props.topologyNodes} />
    </QueryClientProvider>,
  );
}

describe("EndpointPicker", () => {
  it("defaults to the guest-nic tab selected", () => {
    renderPicker({ value: undefined, onChange: vi.fn() });
    expect(screen.getByRole("radio", { name: "Guest NIC" })).toHaveAttribute("aria-checked", "true");
  });

  it("searches guest NICs and reports a selection, filtering out non-guest-nic results", async () => {
    searchInventoryMock.mockResolvedValue({
      results: [
        { ref: "guest-nic:pve1:100/net0", kind: "guest-nic", label: "app01/net0", node: "pve1", matchedField: "name", score: 10 },
        { ref: "guest:pve1:100", kind: "guest", label: "app01", node: "pve1", matchedField: "name", score: 9 },
      ],
    });
    const onChange = vi.fn();
    renderPicker({ value: undefined, onChange });

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Source guest NIC search"), "app01");

    await waitFor(() => {
      expect(screen.getByText("app01/net0")).toBeInTheDocument();
    });
    // Only the guest-nic result renders, not the guest itself.
    expect(screen.queryByText("app01", { selector: "span.truncate" })).not.toBeInTheDocument();

    await user.click(screen.getByText("app01/net0"));
    expect(onChange).toHaveBeenCalledWith({ kind: "guest-nic", ref: "guest-nic:pve1:100/net0" });
  });

  it("shows the selected guest NIC as a chip with a Change affordance", () => {
    renderPicker({ value: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" }, onChange: vi.fn() });
    expect(screen.getByText("guest-nic:pve1:100/net0")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Change" })).toBeInTheDocument();
  });

  it("commits an IP endpoint on Enter", async () => {
    const onChange = vi.fn();
    renderPicker({ value: undefined, onChange });
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "IP address" }));
    await user.type(screen.getByLabelText("Source IP address"), "10.0.0.5{Enter}");
    expect(onChange).toHaveBeenCalledWith({ kind: "ip", ip: "10.0.0.5" });
  });

  it("commits an IP endpoint on blur", async () => {
    const onChange = vi.fn();
    renderPicker({ value: undefined, onChange });
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "IP address" }));
    await user.type(screen.getByLabelText("Source IP address"), "10.0.0.9");
    await user.tab();
    expect(onChange).toHaveBeenCalledWith({ kind: "ip", ip: "10.0.0.9" });
  });

  it("shows subnet context for a typed IP", async () => {
    const nodes: TopologyNode[] = [
      { id: "sdn-subnet::z/v/10.100.0.0/24", kind: "sdn-subnet", label: "10.100.0.0/24", layer: "sdn", nodeGroup: "", status: "ok", badges: [] },
    ];
    renderPicker({ value: undefined, onChange: vi.fn(), topologyNodes: nodes });
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "IP address" }));
    await user.type(screen.getByLabelText("Source IP address"), "10.100.0.5");
    expect(screen.getByText("Within 10.100.0.0/24.")).toBeInTheDocument();
  });

  it("selects external immediately (no further input needed)", async () => {
    const onChange = vi.fn();
    renderPicker({ value: undefined, onChange });
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "External" }));
    expect(onChange).toHaveBeenCalledWith({ kind: "external" });
  });

  it("clears the value when switching away from a filled-in kind", async () => {
    const onChange = vi.fn();
    renderPicker({ value: { kind: "external" }, onChange });
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "Guest NIC" }));
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});
