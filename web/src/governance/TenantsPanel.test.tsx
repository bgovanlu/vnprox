// SPDX-License-Identifier: Apache-2.0

// T-3002 AC3: a tenant-scoped session cannot see an out-of-scope guest
// anywhere in the new screens, asserted on the RENDERED DOM rather than on an
// API response.
//
// What makes this non-vacuous. Scoping is enforced in the daemon, so a scoped
// session's `GET /tenants/{id}` already omits what it may not see; asserting
// that the panel does not render a ref nobody sent it would prove nothing. So
// the query cache is deliberately POISONED first with an unscoped inventory
// payload carrying an out-of-scope guest — the kind of cache a long-lived SPA
// accumulates from other screens — and the assertion is that no panel here
// reaches for it. A guest picker, a "add everything on this node" control, or
// any inventory read added to this screen later would surface that ref and
// fail this test.
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { TenantDetail, TenantListItem } from "../api/tenants";
import { TenantsPanel } from "./TenantsPanel";

const fetchTenants = vi.fn<() => Promise<TenantListItem[]>>();
const fetchTenant = vi.fn<(id: string) => Promise<TenantDetail>>();

vi.mock("../api/tenants", async () => {
  const actual = await vi.importActual<typeof import("../api/tenants")>("../api/tenants");
  return { ...actual, fetchTenants: () => fetchTenants(), fetchTenant: (id: string) => fetchTenant(id) };
});

/** A guest this session's tenant does not own. It exists in the cluster and
 * in some other screen's cache; it must reach no pixel of this one. */
const OUT_OF_SCOPE_GUEST = "guest:pve9:999";
const IN_SCOPE_GUEST = "guest:pve1:101";

const tenantList: TenantListItem[] = [{ id: "t1", name: "Team Blue", createdBy: "root@pam", createdAt: 1_700_000_000 }];

const scopedDetail: TenantDetail = {
  id: "t1",
  name: "Team Blue",
  createdBy: "root@pam",
  createdAt: 1_700_000_000,
  // What a scoped session's daemon answers: its own refs only.
  scopes: [IN_SCOPE_GUEST, "sdn-subnet::10.0.0.0/24"],
  members: [{ identity: "alice@pve", role: "member" }],
};

function renderPanel(): QueryClient {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  // The poison: inventory-shaped cache entries other screens populate, both
  // naming the out-of-scope guest.
  queryClient.setQueryData(["topology"], {
    nodes: [{ id: OUT_OF_SCOPE_GUEST, kind: "guest", label: "vm999" }],
    edges: [],
  });
  queryClient.setQueryData(["guests"], [{ ref: OUT_OF_SCOPE_GUEST, name: "vm999", node: "pve9", vmid: 999 }]);

  render(
    <QueryClientProvider client={queryClient}>
      <TenantsPanel />
    </QueryClientProvider> as ReactNode,
  );
  return queryClient;
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchTenants.mockResolvedValue(tenantList);
  fetchTenant.mockResolvedValue(scopedDetail);
});

describe("TenantsPanel (T-3002 AC3)", () => {
  it("renders no ref the daemon did not give this session — the DOM, not the response", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /Team Blue/ }));
    const scopes = await screen.findByTestId("tenant-scopes");
    expect(within(scopes).getByText(IN_SCOPE_GUEST)).toBeInTheDocument();

    // The whole rendered document, not just this panel's subtree.
    expect(document.body.textContent).not.toContain(OUT_OF_SCOPE_GUEST);
    expect(document.body.textContent).not.toContain("vm999");
    expect(document.body.textContent).not.toContain("pve9");
  });

  it("offers no control that enumerates guests, so there is nothing to leak through", async () => {
    const user = userEvent.setup();
    renderPanel();
    await user.click(await screen.findByRole("button", { name: /Team Blue/ }));
    await screen.findByTestId("tenant-scopes");

    // Adding a scope is a typed Ref. The only <select> on this panel is the
    // member role, whose options are the two roles and nothing else.
    expect(screen.getAllByRole("combobox")).toHaveLength(1);
    const roleSelect = screen.getByRole("combobox", { name: /member role/i });
    expect(within(roleSelect).getAllByRole("option").map((o) => o.textContent)).toEqual(["member", "approver"]);
    expect(screen.getByLabelText(/scope ref/i)).toHaveAttribute("placeholder", "guest:pve1:101");
  });

  it("concludes nothing about scopes or members before the detail read resolves", async () => {
    // The list route reports scopes and members as empty without reading
    // either table, so the panel must not render "no members" from it.
    renderPanel();
    await screen.findByRole("button", { name: /Team Blue/ });
    expect(screen.queryByTestId("tenant-scopes")).toBeNull();
    expect(screen.queryByTestId("tenant-members")).toBeNull();
    expect(screen.getByText(/the list above carries neither/i)).toBeInTheDocument();
    expect(screen.queryByText(/has no members/i)).toBeNull();
    expect(screen.queryByText(/has no scope/i)).toBeNull();
  });

  it("renders a 404 as not found, revealing nothing about the tenant", async () => {
    const user = userEvent.setup();
    fetchTenant.mockRejectedValue(new ApiError(404, "not_found", "no such tenant"));
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /Team Blue/ }));
    const notFound = await screen.findByTestId("tenant-not-found");
    expect(notFound).toHaveTextContent(/no such tenant/i);
    expect(document.body.textContent).not.toContain(OUT_OF_SCOPE_GUEST);
    expect(document.body.textContent).not.toContain(IN_SCOPE_GUEST);
  });

  it("says a genuinely empty scope leaves a tenant's members seeing nothing", async () => {
    const user = userEvent.setup();
    fetchTenant.mockResolvedValue({ ...scopedDetail, scopes: [], members: [] });
    renderPanel();

    await user.click(await screen.findByRole("button", { name: /Team Blue/ }));
    expect(await screen.findByText(/this tenant has no scope, so its members see nothing/i)).toBeInTheDocument();
    expect(screen.getByText(/this tenant has no members/i)).toBeInTheDocument();
  });
});
