// SPDX-License-Identifier: Apache-2.0

// T-2001 AC1-4: attach/edit/detach form logic, credential write-only-ness,
// wgTunnelSource display, and netWrite capability gating — mocked-API unit
// tests mirroring AlertRules.test.tsx's pattern of mocking this feature's
// own query-hooks module so the component exercises real form/validation
// logic against controllable, synchronous mutation stand-ins. The e2e flow
// (attach -> edit -> detach against the real mock daemon) lives in
// web/e2e/federation-clusters.spec.ts.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { MeResponse } from "../api/types";
import type { FederationCluster } from "../api/federation";
import { ToastProvider } from "../components/Toast";
import { FederationClusters } from "./FederationClusters";

const CLUSTER_A: FederationCluster = {
  id: "c1",
  name: "east",
  apiUrl: "https://pve-east.example.com:8006",
  status: "ok",
  addedBy: "root@pam",
  addedAt: 100,
};

const CLUSTER_EXPLICIT: FederationCluster = {
  id: "c2",
  name: "west",
  apiUrl: "https://pve-west.example.com:8006",
  status: "unreachable",
  addedBy: "root@pam",
  addedAt: 200,
  wgTunnelId: "wg-tunnel:pve1:wg0",
  wgTunnelSource: "explicit",
};

const CLUSTER_PEER: FederationCluster = {
  id: "c3",
  name: "south",
  apiUrl: "https://pve-south.example.com:8006",
  status: "unknown",
  addedBy: "root@pam",
  addedAt: 300,
  wgTunnelId: "wg-tunnel:pve1:wg1",
  wgTunnelSource: "peer",
};

let clustersResponse: FederationCluster[] = [CLUSTER_A, CLUSTER_EXPLICIT, CLUSTER_PEER];

const createMutateAsync = vi.fn();
const updateMutateAsync = vi.fn();
const deleteMutateAsync = vi.fn();

vi.mock("./federationClustersQueries", () => ({
  useFederationClustersQuery: () => ({ data: clustersResponse, isLoading: false, error: null }),
  useCreateFederationClusterMutation: () => ({ mutateAsync: createMutateAsync, isPending: false }),
  useUpdateFederationClusterMutation: () => ({ mutateAsync: updateMutateAsync, isPending: false }),
  useDeleteFederationClusterMutation: () => ({ mutateAsync: deleteMutateAsync, isPending: false }),
}));

let mockSession: MeResponse | undefined;
vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    "": {
      netRead: true,
      netWrite: true,
      sdnRead: true,
      sdnWrite: true,
      fwRead: true,
      fwWrite: true,
      guestNet: true,
      audit: true,
      capture: false,
    },
  },
};

const readOnlySession: MeResponse = {
  user: { username: "auditor", realm: "pve" },
  caps: {
    "": {
      netRead: true,
      netWrite: false,
      sdnRead: false,
      sdnWrite: false,
      fwRead: false,
      fwWrite: false,
      guestNet: false,
      audit: true,
      capture: false,
    },
  },
};

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <FederationClusters />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  clustersResponse = [CLUSTER_A, CLUSTER_EXPLICIT, CLUSTER_PEER];
  mockSession = fullSession;
  createMutateAsync.mockReset().mockResolvedValue({ ...CLUSTER_A, id: "new-id" });
  updateMutateAsync.mockReset().mockResolvedValue(CLUSTER_A);
  deleteMutateAsync.mockReset().mockResolvedValue(undefined);
});

describe("FederationClusters list + selection", () => {
  it("lists every attached cluster", () => {
    renderPage();
    expect(screen.getByRole("button", { name: /east/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /west/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /south/ })).toBeInTheDocument();
  });

  it("selecting a cluster populates the edit form with its name/apiUrl but no credential fields", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /east/ }));

    expect(screen.getByLabelText("Name")).toHaveValue("east");
    expect(screen.getByLabelText("API URL")).toHaveValue("https://pve-east.example.com:8006");
    expect(screen.getByLabelText("Username")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveValue("");
  });
});

describe("FederationClusters attach form validation", () => {
  it("disables Attach when name is empty", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Attach cluster" }));

    await user.type(screen.getByLabelText("API URL"), "https://example.com:8006");
    expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();
    expect(screen.getByText("Name is required.")).toBeInTheDocument();
  });

  it("disables Attach when the API URL is not absolute http(s)", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Attach cluster" }));

    await user.type(screen.getByLabelText("Name"), "north");
    await user.type(screen.getByLabelText("API URL"), "not-a-url");
    expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();
    expect(screen.getByText(/absolute http\(s\) URL/)).toBeInTheDocument();
  });

  it("requires a credential to attach a new cluster", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Attach cluster" }));

    await user.type(screen.getByLabelText("Name"), "north");
    await user.type(screen.getByLabelText("API URL"), "https://north.example.com:8006");
    expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();
    expect(screen.getByText("Username is required.")).toBeInTheDocument();
  });

  it("submits a valid new cluster with a ticket credential", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Attach cluster" }));

    await user.type(screen.getByLabelText("Name"), "north");
    await user.type(screen.getByLabelText("API URL"), "https://north.example.com:8006");
    await user.type(screen.getByLabelText("Username"), "root@pam");
    await user.type(screen.getByLabelText("Password"), "s3cret");
    const attachButton = screen.getByRole("button", { name: "Attach" });
    expect(attachButton).not.toBeDisabled();
    await user.click(attachButton);

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledWith({
        name: "north",
        apiUrl: "https://north.example.com:8006",
        credential: { kind: "ticket", username: "root@pam", password: "s3cret", realm: undefined },
      });
    });
  });

  it("submits a token credential when that kind is selected", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Attach cluster" }));

    await user.type(screen.getByLabelText("Name"), "north");
    await user.type(screen.getByLabelText("API URL"), "https://north.example.com:8006");
    await user.click(screen.getByRole("radio", { name: "API token" }));
    await user.type(screen.getByLabelText("Token"), "root@pam!id=secretvalue");
    await user.click(screen.getByRole("button", { name: "Attach" }));

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({ credential: { kind: "token", token: "root@pam!id=secretvalue" } }),
      );
    });
  });
});

describe("FederationClusters credential is write-only", () => {
  it("never renders a previously-set credential value back, and an edit that leaves it unchanged sends no credential field", async () => {
    const user = userEvent.setup();
    const { container } = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ToastProvider>
          <FederationClusters />
        </ToastProvider>
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: /east/ }));

    // The credential fields are always blank on selection — the API never
    // returns the credential, so there is nothing to leak into the DOM.
    expect(screen.getByLabelText("Username")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(container.innerHTML).not.toMatch(/s3cret|hunter2|vnprox-mock/);

    // Rename only, without touching the credential fields, then save.
    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "east-renamed");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(updateMutateAsync).toHaveBeenCalledWith({
        id: "c1",
        req: { name: "east-renamed", apiUrl: "https://pve-east.example.com:8006" },
      });
    });
    const call = updateMutateAsync.mock.calls[0] as [{ req: Record<string, unknown> }];
    expect(call[0].req).not.toHaveProperty("credential");

    // Still nothing credential-shaped anywhere in the DOM after submission.
    expect(container.innerHTML).not.toMatch(/s3cret|hunter2|vnprox-mock/);
  });

  it("includes a credential only when the operator explicitly types a new one during an edit", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /east/ }));
    await user.type(screen.getByLabelText("Username"), "newuser@pam");
    await user.type(screen.getByLabelText("Password"), "newpass");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(updateMutateAsync).toHaveBeenCalledWith({
        id: "c1",
        req: {
          name: "east",
          apiUrl: "https://pve-east.example.com:8006",
          credential: { kind: "ticket", username: "newuser@pam", password: "newpass", realm: undefined },
        },
      });
    });
  });
});

describe("FederationClusters tunnel linkage (wgTunnelSource)", () => {
  it("shows no linkage for a cluster with none", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /east/ }));
    expect(screen.getByTestId("tunnel-linkage")).toHaveTextContent("Not tunnel-linked");
  });

  it("distinguishes an explicit override from a peer-derived link, and states the clearing consequence", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: /west/ }));
    const explicitPanel = screen.getByTestId("tunnel-linkage");
    expect(explicitPanel).toHaveTextContent("wg-tunnel:pve1:wg0");
    expect(explicitPanel).toHaveTextContent("explicit override");
    expect(explicitPanel).toHaveTextContent(/does not.*unlink/i);
    expect(within(explicitPanel).getByRole("button", { name: "Clear override" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /south/ }));
    const peerPanel = screen.getByTestId("tunnel-linkage");
    expect(peerPanel).toHaveTextContent("wg-tunnel:pve1:wg1");
    expect(peerPanel).toHaveTextContent("derived from tagged peer");
    expect(within(peerPanel).queryByRole("button", { name: "Clear override" })).not.toBeInTheDocument();
  });

  it("clearing an explicit override sends wgTunnelId: '' and leaves name/apiUrl/credential alone", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /west/ }));
    await user.click(screen.getByRole("button", { name: "Clear override" }));

    await waitFor(() => {
      expect(updateMutateAsync).toHaveBeenCalledWith({
        id: "c2",
        req: { name: "west", apiUrl: "https://pve-west.example.com:8006", wgTunnelId: "" },
      });
    });
  });
});

describe("FederationClusters detach", () => {
  it("shows a confirmation naming what is lost before detaching", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /east/ }));
    await user.click(screen.getByRole("button", { name: "Detach" }));

    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/aggregated global topology/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/cross-cluster IPAM conflict detection/i)).toBeInTheDocument();
    expect(deleteMutateAsync).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: "Detach" }));

    await waitFor(() => {
      expect(deleteMutateAsync).toHaveBeenCalledWith("c1");
    });
  });

  it("cancelling the confirmation does not detach", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: /east/ }));
    await user.click(screen.getByRole("button", { name: "Detach" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(deleteMutateAsync).not.toHaveBeenCalled();
  });
});

describe("FederationClusters read-only gating", () => {
  it("disables Attach cluster / Save / Detach / Clear override for a netWrite-less session, but the read view still works", async () => {
    mockSession = readOnlySession;
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByRole("button", { name: "Attach cluster" })).toBeDisabled();
    expect(screen.getByRole("button", { name: /east/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /west/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /west/ }));
    expect(screen.getByLabelText("Name")).toHaveValue("west");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Detach" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Clear override" })).toBeDisabled();
  });
});
