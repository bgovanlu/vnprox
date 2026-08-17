// T-3106 acceptance criterion 4: proves the i18n pipeline round-trips
// end-to-end — real bundled `fr` resources, real i18next pluralization,
// a real <Trans> interpolation, and a real Testing Library assertion that
// switching the active locale changes rendered text (not a manual eyeball
// check). English remains the only *shipped* locale (see
// web/src/i18n/i18n.ts's doc comment: no browser-language detection, no
// user-facing switcher) — this test is the one place `fr` is reachable at
// all, via `i18n.changeLanguage("fr")` directly, exactly the "reachable
// only from a test" shape the card asked for.
//
// Uses the same mock-every-api-boundary convention as
// OnboardingWalkthrough.test.tsx (kept intentionally minimal here — only
// the found-summary step's own queries are exercised). Resets the shared
// i18next singleton back to "en" in afterEach so no other test file's
// assertions (which all expect the shipped English strings) can observe a
// locale left switched by this one.
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { OnboardingProgress, TopologyResponse } from "../api/types";
import i18n from "../i18n/i18n";
import { OnboardingWalkthrough } from "./OnboardingWalkthrough";
import { freshOnboardingProgress } from "./onboardingMachine";

vi.mock("../api/onboarding", () => ({
  fetchOnboardingProgress: vi.fn(() =>
    Promise.resolve({ name: "onboarding", layout: freshOnboardingProgress(), updatedAt: 0 }),
  ),
  saveOnboardingProgress: vi.fn((progress: OnboardingProgress) =>
    Promise.resolve({ name: "onboarding", layout: progress, updatedAt: 0 }),
  ),
}));

vi.mock("../api/useSession", () => ({
  useSession: () => ({
    data: {
      user: { username: "root", realm: "pam" },
      caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
    },
  }),
}));

const mockTopology: TopologyResponse = { nodes: [], edges: [], layers: ["phys", "l2", "sdn", "guest"], generatedAt: 0 };
vi.mock("../api/topology", () => ({
  fetchTopology: vi.fn(() => Promise.resolve(mockTopology)),
  fetchInventoryDetail: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/drift", () => ({
  fetchDrift: vi.fn(() => Promise.resolve([])),
  fixDriftFinding: vi.fn(),
}));

vi.mock("../api/protectedInterfaces", () => ({
  fetchProtectedInterfaces: vi.fn(() => Promise.resolve({ nodes: {}, updatedAt: 0, version: 0 })),
  fetchProtectedInterfacesSuggest: vi.fn(() => Promise.resolve({ nodes: {} })),
  saveProtectedInterfaces: vi.fn(),
}));

vi.mock("../api/lldp", () => ({
  fetchLldpNeighbors: vi.fn(() => Promise.resolve({ items: [] })),
  installLldp: vi.fn(),
}));

function renderWalkthrough(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <OnboardingWalkthrough />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("onboarding i18n pipeline round-trip (T-3106 acceptance criterion 4)", () => {
  afterEach(async () => {
    vi.clearAllMocks();
    // English is the only shipped locale — never leave a later test file
    // observing a switched-away instance.
    await i18n.changeLanguage("en");
  });

  it("renders the shipped English copy by default", async () => {
    renderWalkthrough();
    expect(await screen.findByText("What we found")).toBeInTheDocument();
    expect(screen.getByText(/Your cluster's network, drawn\./)).toBeInTheDocument();
  });

  it("switching the active locale to fr changes the rendered text", async () => {
    await i18n.changeLanguage("fr");
    renderWalkthrough();

    // The step title, translated.
    expect(await screen.findByText("Ce que nous avons trouvé")).toBeInTheDocument();
    // The intro sentence, translated (proves a full-paragraph t() key, not
    // just a short label).
    expect(screen.getByText(/Le réseau de votre cluster, dessiné\./)).toBeInTheDocument();
    // The button, translated.
    expect(screen.getByRole("button", { name: "Continuer" })).toBeInTheDocument();
    // The English strings are gone, not just supplemented.
    expect(screen.queryByText("What we found")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();

    await waitFor(() => {
      // The "0 node(s): none detected" interpolation, translated
      // (french "aucun détecté" for the empty-list fallback), proving the
      // interpolation values thread through a locale switch too.
      expect(screen.getByText(/aucun détecté/)).toBeInTheDocument();
    });
  });
});
