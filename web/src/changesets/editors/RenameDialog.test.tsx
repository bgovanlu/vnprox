import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../../components/Toast";
import type { MeResponse } from "../../api/types";
import { RenameDialog } from "./RenameDialog";

const me: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { pve1: { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

function renderDialog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <RenameDialog open onOpenChange={() => undefined} node="pve1" target="bridge:pve1:vmbr0" currentName="vmbr0" />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("RenameDialog", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response(JSON.stringify(me), { status: 200, headers: { "Content-Type": "application/json" } }))),
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows an inline error for an illegal name and no red-asterisk preview", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(screen.getByRole("textbox", { name: /New name/ }), "bad name");
    expect(screen.getByRole("alert")).toHaveTextContent(/no spaces or slashes/i);
  });

  it("shows the staged new name with a red-asterisk temp marker for a valid name", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(screen.getByRole("textbox", { name: /New name/ }), "vmbrmgmt");
    expect(screen.getByText("vmbrmgmt")).toBeInTheDocument();
    // The asterisk carries the temporary-until-reboot inline help.
    expect(screen.getByTitle(/Temporary until applied/i)).toBeInTheDocument();
  });
});
