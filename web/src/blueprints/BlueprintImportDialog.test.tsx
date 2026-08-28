// SPDX-License-Identifier: Apache-2.0

// T-1107 AC6: BlueprintImportDialog's three signature-status states
// (verified/trusted, signed-but-untrusted, unsigned) each render distinctly,
// and the explicit trust step (a checkbox) gates the "Import anyway" action
// for the latter two — it must never be possible to import an unsigned or
// untrusted-signed bundle without checking it first. The backend is mocked
// at the api/blueprints.ts boundary (the same pattern ParamForm.test.tsx
// uses), so this exercises the real useImportBundleMutation/react-query
// wiring, not a hand-rolled fake hook.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Blueprint, BlueprintBundle, ImportBundleRequest, ImportBundleResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { BlueprintImportDialog } from "./BlueprintImportDialog";

const importBlueprintBundle = vi.fn<(req: ImportBundleRequest) => Promise<ImportBundleResponse>>();

vi.mock("../api/blueprints", () => ({
  importBlueprintBundle: (req: ImportBundleRequest) => importBlueprintBundle(req),
}));

function renderWithProviders(ui: ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>{ui}</ToastProvider>
    </QueryClientProvider>,
  );
}

const BP: Blueprint = {
  blueprintVersion: 1,
  id: "shared-bp",
  name: "Shared blueprint",
  nodeSelector: { mode: "all" },
  params: [],
  entities: [{ kind: "bridge", idTemplate: "vmbr9", fields: { vlanAware: true } }],
};

const UNSIGNED_BUNDLE: BlueprintBundle = { bundleVersion: 1, blueprint: BP };
const SIGNED_BUNDLE: BlueprintBundle = {
  bundleVersion: 1,
  blueprint: BP,
  signature: { alg: "ed25519", publicKeyFingerprint: "abc123", publicKey: "cHVia2V5", sig: "c2ln" },
};

function renderDialog(bundle: BlueprintBundle, onImported = vi.fn()) {
  const onOpenChange = vi.fn();
  renderWithProviders(
    <BlueprintImportDialog open bundle={bundle} onOpenChange={onOpenChange} onImported={onImported} />,
  );
  return { onOpenChange, onImported };
}

describe("BlueprintImportDialog", () => {
  beforeEach(() => {
    importBlueprintBundle.mockReset();
  });

  it("verified/trusted: imports immediately with no prompt and no trust step", async () => {
    importBlueprintBundle.mockResolvedValueOnce({ status: "imported", blueprint: { ...BP, id: "new-id" } });
    const { onImported, onOpenChange } = renderDialog(SIGNED_BUNDLE);

    await waitFor(() => {
      expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: "new-id" }));
    });
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(importBlueprintBundle).toHaveBeenCalledTimes(1);
    // No trust-step checkbox or "Import anyway" button ever appears.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /import anyway/i })).not.toBeInTheDocument();
  });

  it("signed-but-untrusted: shows the signer and gates import behind the trust checkbox", async () => {
    importBlueprintBundle.mockResolvedValueOnce({
      status: "untrustedSignature",
      signer: { fingerprint: "abc123", publicKey: "cHVia2V5" },
    });
    const user = userEvent.setup();
    const { onImported } = renderDialog(SIGNED_BUNDLE);

    await screen.findByTestId("import-state-untrusted");
    expect(screen.getByText(/abc123/)).toBeInTheDocument();

    const importButton = screen.getByRole("button", { name: /import anyway/i });
    expect(importButton).toBeDisabled();

    const checkbox = screen.getByRole("checkbox");
    await user.click(checkbox);
    expect(importButton).toBeEnabled();

    importBlueprintBundle.mockResolvedValueOnce({ status: "imported", blueprint: { ...BP, id: "new-id-2" } });
    await user.click(importButton);

    await waitFor(() => {
      expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: "new-id-2" }));
    });
    // The confirm call carried the trust decision.
    expect(importBlueprintBundle).toHaveBeenLastCalledWith(expect.objectContaining({ trustNewKey: true }));
  });

  it("unsigned: gates import behind the trust checkbox and never imports without it", async () => {
    importBlueprintBundle.mockResolvedValueOnce({ status: "unsigned" });
    const user = userEvent.setup();
    const { onImported } = renderDialog(UNSIGNED_BUNDLE);

    await screen.findByTestId("import-state-unsigned");
    const importButton = screen.getByRole("button", { name: /import anyway/i });
    expect(importButton).toBeDisabled();

    // Clicking a disabled button must not fire the handler at all.
    await user.click(importButton);
    expect(importBlueprintBundle).toHaveBeenCalledTimes(1); // only the initial probe
    expect(onImported).not.toHaveBeenCalled();

    const checkbox = screen.getByRole("checkbox");
    await user.click(checkbox);
    expect(importButton).toBeEnabled();

    importBlueprintBundle.mockResolvedValueOnce({ status: "imported", blueprint: { ...BP, id: "new-id-3" } });
    await user.click(importButton);

    await waitFor(() => {
      expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: "new-id-3" }));
    });
    expect(importBlueprintBundle).toHaveBeenLastCalledWith(expect.objectContaining({ trustUnsigned: true }));
  });

  it("invalidSignature: never offers an import action at all", async () => {
    importBlueprintBundle.mockResolvedValueOnce({ status: "invalidSignature" });
    const { onImported } = renderDialog(SIGNED_BUNDLE);

    await screen.findByTestId("import-state-invalid");
    expect(screen.queryByRole("button", { name: /import anyway/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(onImported).not.toHaveBeenCalled();
  });
});
