import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../components/Toast";
import type { IpamCell } from "../api/types";
import type { GuestNicRow } from "../guests/guestNics";
import { CellDetailDialog } from "./CellDetailDialog";

// T-406's MAC-picker proof-of-interface (mirroring T-405's BridgeEditor.
// test.tsx precedent for NextFreePicker): the reservation flow's MAC field
// can be filled either by hand or by picking a real guest NIC from
// inventory. useAllGuestNicsQuery is mocked directly (rather than
// fetch-stubbing the full GET /topology response it composes from) to
// keep this test scoped to CellDetailDialog+MacPicker's own wiring.
const mockRows: GuestNicRow[] = [
  { ref: "guest-nic:pve1:300/net0", label: "web1/net0", node: "pve1", mac: "AA:BB:CC:DD:EE:01", linkDown: false },
];
vi.mock("../guests/queries", () => ({
  useAllGuestNicsQuery: () => ({ rows: mockRows, isLoading: false }),
}));

const addOps = vi.fn().mockResolvedValue({ id: "cs1" });
vi.mock("../changesets/useDrawerActions", () => ({
  useDrawerActions: () => ({ addOps, replaceOps: vi.fn(), amendLastOps: vi.fn() }),
}));

const freeCell: IpamCell = { ip: "10.50.0.222", state: "free" };

function renderDialog(cell: IpamCell = freeCell) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <CellDetailDialog open cell={cell} subnetCidr="10.50.0.0/24" onOpenChange={vi.fn()} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("CellDetailDialog reservation MAC picker (T-406)", () => {
  it("picking a guest NIC fills the MAC field and prefills the hostname", async () => {
    renderDialog();

    const picker = screen.getByRole("combobox", { name: "Pick a guest MAC" });
    await userEvent.selectOptions(picker, "AA:BB:CC:DD:EE:01");

    const macInput = screen.getByPlaceholderText<HTMLInputElement>("MAC (optional)");
    expect(macInput.value).toBe("AA:BB:CC:DD:EE:01");
    const hostnameInput = screen.getByPlaceholderText<HTMLInputElement>("Hostname (optional)");
    expect(hostnameInput.value).toBe("web1/net0");
  });

  it("reserving after picking a MAC lands an ipam.alloc.create op with that MAC bound — the guest-MAC-bound reservation itself", async () => {
    renderDialog();

    const picker = screen.getByRole("combobox", { name: "Pick a guest MAC" });
    await userEvent.selectOptions(picker, "AA:BB:CC:DD:EE:01");

    await userEvent.click(screen.getByRole("button", { name: "Reserve 10.50.0.222" }));

    await waitFor(() => {
      expect(addOps).toHaveBeenCalledTimes(1);
    });
    const [ops] = addOps.mock.calls[0] as [{ op: string; params: { mac?: string; hostname?: string; cidr: string } }[]];
    expect(ops).toHaveLength(1);
    expect(ops[0]?.op).toBe("ipam.alloc.create");
    expect(ops[0]?.params.mac).toBe("AA:BB:CC:DD:EE:01");
    expect(ops[0]?.params.cidr).toBe("10.50.0.222/32");
  });

  it("does not overwrite a hostname the user already typed", async () => {
    renderDialog();

    const hostnameInput = screen.getByPlaceholderText<HTMLInputElement>("Hostname (optional)");
    await userEvent.type(hostnameInput, "manually-typed");

    const picker = screen.getByRole("combobox", { name: "Pick a guest MAC" });
    await userEvent.selectOptions(picker, "AA:BB:CC:DD:EE:01");

    expect(hostnameInput.value).toBe("manually-typed");
  });
});
