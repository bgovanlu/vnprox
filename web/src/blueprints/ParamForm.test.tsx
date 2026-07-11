// Component-level test for the blueprint param form: T-603 AC4's "bad
// CIDR/VID rejected at the form" and the "next-free suggestion" wiring.
// The backend is mocked at the api/blueprints.ts boundary.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { BlueprintParamDef } from "../api/types";
import { ParamForm } from "./ParamForm";

vi.mock("../api/blueprints", () => ({
  suggestBlueprintAddress: vi.fn(() => Promise.resolve({ address: "192.168.1.42/24" })),
}));

function renderWithProviders(ui: ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const PARAMS: BlueprintParamDef[] = [
  { name: "bridgeName", type: "string", label: "Bridge name", required: true, default: "vmbr0" },
  { name: "mgmtCidr", type: "cidr", label: "Management address", required: true, default: "192.168.1.10/24", addressSuggest: true },
  { name: "guestVlans", type: "vidList", label: "Guest VLANs", required: true, default: [10, 20, 30] },
];

describe("ParamForm", () => {
  it("submits parsed params when every field is valid", async () => {
    const user = userEvent.setup();
    const onValidSubmit = vi.fn();
    renderWithProviders(
      <ParamForm
        blueprintId="bp1"
        params={PARAMS}
        nodesValue=""
        onNodesChange={() => undefined}
        onValidSubmit={onValidSubmit}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Instantiate" }));

    expect(onValidSubmit).toHaveBeenCalledWith({
      bridgeName: "vmbr0",
      mgmtCidr: "192.168.1.10/24",
      guestVlans: [10, 20, 30],
    });
  });

  it("blocks submission and shows an error for a bad CIDR (AC4)", async () => {
    const user = userEvent.setup();
    const onValidSubmit = vi.fn();
    renderWithProviders(
      <ParamForm
        blueprintId="bp1"
        params={PARAMS}
        nodesValue=""
        onNodesChange={() => undefined}
        onValidSubmit={onValidSubmit}
      />,
    );

    const cidrInput = screen.getByLabelText(/Management address/);
    await user.clear(cidrInput);
    await user.type(cidrInput, "not-a-cidr");
    await user.click(screen.getByRole("button", { name: "Instantiate" }));

    expect(onValidSubmit).not.toHaveBeenCalled();
    expect(await screen.findByText(/must be a CIDR address/)).toBeInTheDocument();
  });

  it("blocks submission and shows an error for a bad VID (AC4)", async () => {
    const user = userEvent.setup();
    const onValidSubmit = vi.fn();
    renderWithProviders(
      <ParamForm
        blueprintId="bp1"
        params={PARAMS}
        nodesValue=""
        onNodesChange={() => undefined}
        onValidSubmit={onValidSubmit}
      />,
    );

    const vlansInput = screen.getByLabelText(/Guest VLANs/);
    await user.clear(vlansInput);
    await user.type(vlansInput, "10, 9999");
    await user.click(screen.getByRole("button", { name: "Instantiate" }));

    expect(onValidSubmit).not.toHaveBeenCalled();
    expect(await screen.findByText(/not a valid VLAN id/)).toBeInTheDocument();
  });

  it("fills the field with a suggested address (AC4)", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ParamForm blueprintId="bp1" params={PARAMS} nodesValue="" onNodesChange={() => undefined} onValidSubmit={() => undefined} />,
    );

    await user.click(screen.getByRole("button", { name: "Suggest" }));

    await waitFor(() => {
      expect(screen.getByLabelText(/Management address/)).toHaveValue("192.168.1.42/24");
    });
  });
});
