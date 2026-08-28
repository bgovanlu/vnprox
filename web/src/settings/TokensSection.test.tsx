// SPDX-License-Identifier: Apache-2.0

// T-3003 AC1: a token minted through the UI carries the default 90-day
// expiry, and the list distinguishes stored scope from effective scope under
// `read_only`.
//
// These tests mock the API client module (`../api/tokens`) rather than this
// feature's own query hooks, so the real TanStack mutation runs and the exact
// request body reaching the wire is observable — which is the only way to
// assert the "default" half of AC1. Substituting the hooks would let the test
// agree with a request the product never makes.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiToken, ApiTokenCreateRequest, ApiTokenCreateResponse, InstanceConfigResponse, MeResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { TokensSection } from "./TokensSection";

const fetchTokens = vi.fn<() => Promise<ApiToken[]>>();
const mintToken = vi.fn<(req: ApiTokenCreateRequest) => Promise<ApiTokenCreateResponse>>();
const revokeToken = vi.fn<(id: string) => Promise<void>>();

vi.mock("../api/tokens", () => ({
  fetchTokens: (...args: []) => fetchTokens(...args),
  mintToken: (req: ApiTokenCreateRequest) => mintToken(req),
  revokeToken: (id: string) => revokeToken(id),
}));

let mockSession: MeResponse | undefined;
vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

let mockConfig: InstanceConfigResponse | undefined;
vi.mock("./queries", () => ({
  useInstanceConfigQuery: () => ({ data: mockConfig }),
}));

const FULL_SESSION: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: {
    "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false },
  },
};

function config(readOnly: boolean): InstanceConfigResponse {
  return {
    version: "4.1.0",
    listen: "0.0.0.0:8007",
    pveApiUrl: "https://pve1:8006",
    protectedPath: "/etc/vnprox/protected.json",
    pveInterval: "30s",
    hostInterval: "5s",
    lldpInterval: "60s",
    confirmTimeoutDefaultSec: 120,
    snapshotKeepDays: 30,
    snapshotPinDays: 365,
    readOnly,
    allowDangerousOps: false,
    demo: false,
  };
}

const NINETY_DAYS = 90 * 24 * 60 * 60;

function renderSection(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <TokensSection />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockSession = FULL_SESSION;
  mockConfig = config(false);
  fetchTokens.mockResolvedValue([]);
  mintToken.mockReset();
  revokeToken.mockReset();
});

describe("minting", () => {
  it("omits expiresAt so the daemon applies its own 90-day default, and shows back the instant it chose", async () => {
    const user = userEvent.setup();
    const expiresAt = Math.floor(Date.now() / 1000) + NINETY_DAYS;
    mintToken.mockResolvedValue({
      id: "t9",
      name: "terraform-ci",
      scopes: ["netRead"],
      createdBy: "root@pam",
      createdAt: Math.floor(Date.now() / 1000),
      expiresAt,
      token: "vnx_secret_value",
    });

    renderSection();
    await screen.findByTestId("tokens-empty");

    await user.type(screen.getByPlaceholderText("terraform-ci"), "terraform-ci");
    await user.click(screen.getByRole("checkbox", { name: /netRead/ }));
    await user.click(screen.getByTestId("mint-token"));

    await waitFor(() => {
      expect(mintToken).toHaveBeenCalledTimes(1);
    });
    const req = mintToken.mock.calls[0]?.[0];
    expect(req).toEqual({ name: "terraform-ci", scopes: ["netRead"] });
    // The load-bearing assertion: the property is ABSENT, not null and not a
    // client-computed timestamp. Absent is what selects defaultTokenTTL; null
    // would mint a non-expiring token, which is the opposite outcome.
    expect(req !== undefined && "expiresAt" in req).toBe(false);

    // …and the reveal reports the daemon's answer, not a recomputed one.
    const reveal = await screen.findByTestId("minted-token");
    expect(within(reveal).getByTestId("minted-token-value")).toHaveTextContent("vnx_secret_value");
    expect(within(reveal).getByTestId("minted-token-expiry")).toHaveTextContent(
      new Date(expiresAt * 1000).toLocaleString(),
    );
  });

  it("sends an explicit null only when 'never expires' is chosen", async () => {
    const user = userEvent.setup();
    mintToken.mockResolvedValue({
      id: "t10",
      name: "forever",
      scopes: [],
      createdBy: "root@pam",
      createdAt: 1,
      token: "raw",
    });

    renderSection();
    await screen.findByTestId("tokens-empty");

    await user.type(screen.getByPlaceholderText("terraform-ci"), "forever");
    await user.click(screen.getByRole("radio", { name: /Never expires/ }));
    await user.click(screen.getByTestId("mint-token"));

    await waitFor(() => {
      expect(mintToken).toHaveBeenCalledWith({ name: "forever", scopes: [], expiresAt: null });
    });
    expect(await screen.findByTestId("minted-token-expiry")).toHaveTextContent("does not expire");
  });

  it("disables a scope the session does not hold, and never disables automation", async () => {
    mockSession = {
      user: { username: "auditor", realm: "pve" },
      caps: {
        "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true, capture: false },
      },
    };
    renderSection();
    await screen.findByTestId("tokens-empty");

    expect(screen.getByRole("checkbox", { name: /netRead/ })).toBeEnabled();
    expect(screen.getByRole("checkbox", { name: /netWrite/ })).toBeDisabled();
    // automation/automationWrite are never derived from a PVE privilege, so
    // both are always grantable — Identity.CanGrantScope short-circuits
    // them. Exact-match the accessible name: "automation" is itself a
    // substring of "automationWrite", so a loose /automation/ match would
    // hit both checkboxes and fail on multiple matches.
    expect(screen.getByRole("checkbox", { name: "automation" })).toBeEnabled();
    expect(screen.getByRole("checkbox", { name: "automationWrite" })).toBeEnabled();
  });
});

describe("stored scope vs effective scope", () => {
  const writeToken: ApiToken = {
    id: "t1",
    name: "ci",
    scopes: ["netRead", "netWrite", "guestNet", "capture"],
    createdBy: "root@pam",
    createdAt: 1,
  };

  it("says the two are the same in a writable deployment", async () => {
    mockConfig = config(false);
    fetchTokens.mockResolvedValue([writeToken]);
    renderSection();

    const cell = await screen.findByTestId("effective-scope-t1");
    expect(cell).toHaveAttribute("data-scope-state", "same");
  });

  // T-3003-followup-01 (2026-08-19): capture is no longer left alone by
  // read_only — it is now stripped outright, alongside the original four
  // config-write flags. This test replaces "names the scopes read_only
  // removes, and leaves capture alone", which pinned the pre-fix behaviour.
  it("names the scopes read_only removes, including capture", async () => {
    mockConfig = config(true);
    fetchTokens.mockResolvedValue([writeToken]);
    renderSection();

    const cell = await screen.findByTestId("effective-scope-t1");
    expect(cell).toHaveAttribute("data-scope-state", "narrowed");
    expect(cell).toHaveTextContent("netWrite, guestNet, capture removed");
    // The stored scope is still shown in full, which is the whole point: an
    // operator must be able to see both at once.
    const row = screen.getByTestId("token-row-t1");
    expect(row).toHaveTextContent("netWrite");
    expect(screen.getByTestId("tokens-read-only-banner")).toBeInTheDocument();
  });

  it("reports the effective scope as UNKNOWN while the instance config has not loaded", async () => {
    mockConfig = undefined;
    fetchTokens.mockResolvedValue([writeToken]);
    renderSection();

    const cell = await screen.findByTestId("effective-scope-t1");
    expect(cell).toHaveAttribute("data-scope-state", "unknown");
    expect(cell).toHaveTextContent(/Unknown/);
    // It must not claim the two are the same, which is what a two-valued
    // boolean would have produced here.
    expect(cell).not.toHaveAttribute("data-scope-state", "same");
  });
});

describe("expiry rendering", () => {
  it("renders never / future / expired as three distinct states", async () => {
    const now = Math.floor(Date.now() / 1000);
    fetchTokens.mockResolvedValue([
      { id: "never", name: "legacy", scopes: [], createdBy: "root@pam", createdAt: 1 },
      { id: "future", name: "live", scopes: [], createdBy: "root@pam", createdAt: 1, expiresAt: now + 1000 },
      { id: "past", name: "stale", scopes: [], createdBy: "root@pam", createdAt: 1, expiresAt: now - 1000 },
    ]);
    renderSection();

    expect(await screen.findByTestId("expiry-never")).toHaveAttribute("data-expiry-state", "never");
    expect(screen.getByTestId("expiry-never")).toHaveTextContent("Never expires");
    expect(screen.getByTestId("expiry-future")).toHaveAttribute("data-expiry-state", "expires");
    expect(screen.getByTestId("expiry-past")).toHaveAttribute("data-expiry-state", "expired");

    // A pre-v4.1 token with no expiry is active, not unknown and not expired.
    expect(screen.getByTestId("lifecycle-never")).toHaveTextContent("active");
    expect(screen.getByTestId("lifecycle-past")).toHaveTextContent("expired");
  });
});

describe("refusals", () => {
  it("renders the daemon's own message when minting is refused", async () => {
    const user = userEvent.setup();
    const { ApiError } = await import("../api/client");
    mintToken.mockRejectedValue(new ApiError(403, "forbidden", "scope exceeds capabilities: sdnWrite"));

    renderSection();
    await screen.findByTestId("tokens-empty");
    await user.type(screen.getByPlaceholderText("terraform-ci"), "bad");
    await user.click(screen.getByTestId("mint-token"));

    const notice = await screen.findByTestId("mint-token-error");
    expect(notice).toHaveAttribute("data-refusal-kind", "forbidden");
    expect(screen.getByTestId("mint-token-error-message")).toHaveTextContent("scope exceeds capabilities: sdnWrite");
  });
});
