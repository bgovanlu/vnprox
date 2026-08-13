// Component tests for T-2005's push settings surface. The backend is
// mocked at the api/push.ts boundary (matching every other component test
// in this codebase, e.g. FindingsStreamPanel.test.tsx); the browser Push
// API is mocked at browserPush.ts's boundary since jsdom has no real
// PushManager/ServiceWorkerRegistration.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { PushSubscriptionSummary } from "../api/push";
import { PushSettingsSection } from "./PushSettingsSection";

const fetchVapidPublicKey = vi.fn(() => Promise.resolve("test-vapid-key"));
const fetchPushSubscriptions = vi.fn((): Promise<PushSubscriptionSummary[]> => Promise.resolve([]));
const createPushSubscription = vi.fn(
  (
    _subscription: { endpoint: string; keys: { p256dh: string; auth: string } },
    _categories: string[],
    _deviceLabel?: string,
  ): Promise<PushSubscriptionSummary> =>
    Promise.resolve({ id: "push-new", categories: ["critical", "awaitingConfirm"], deviceLabel: "Test device", createdAt: 1000 }),
);
const deletePushSubscription = vi.fn((_id: string) => Promise.resolve());

vi.mock("../api/push", async () => {
  const actual = await vi.importActual<typeof import("../api/push")>("../api/push");
  return {
    ...actual,
    fetchVapidPublicKey: () => fetchVapidPublicKey(),
    fetchPushSubscriptions: () => fetchPushSubscriptions(),
    createPushSubscription: (...args: unknown[]) =>
      createPushSubscription(...(args as Parameters<typeof createPushSubscription>)),
    deletePushSubscription: (id: string) => deletePushSubscription(id),
  };
});

const isPushSupported = vi.fn(() => true);
const getPushBrowserStatus = vi.fn(() => Promise.resolve({ supported: true, permission: "default", subscription: null }));
const requestPushSubscription = vi.fn((_key: string) =>
  Promise.resolve({
    toJSON: () => ({ endpoint: "https://push.example/send/abc", keys: { p256dh: "p", auth: "a" } }),
  } as unknown as PushSubscription),
);
const unsubscribeBrowserPush = vi.fn(() => Promise.resolve(true));

vi.mock("./browserPush", () => ({
  isPushSupported: () => isPushSupported(),
  getPushBrowserStatus: () => getPushBrowserStatus(),
  requestPushSubscription: (key: string) => requestPushSubscription(key),
  unsubscribeBrowserPush: () => unsubscribeBrowserPush(),
}));

function renderSection(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <PushSettingsSection />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("PushSettingsSection", () => {
  it("shows an unsupported message when the browser has no Push API", async () => {
    isPushSupported.mockReturnValueOnce(false);
    renderSection();
    expect(await screen.findByText(/does not support push notifications/i)).toBeInTheDocument();
    expect(screen.queryByText("Critical findings")).not.toBeInTheDocument();
  });

  it("enables push with the selected categories and remembers the returned subscription id", async () => {
    const user = userEvent.setup();
    renderSection();

    await screen.findByText("Critical findings");
    // Drift is unchecked by default (only critical + awaitingConfirm are).
    await user.click(screen.getByRole("checkbox", { name: /configuration drift/i }));

    await user.click(screen.getByRole("button", { name: /enable push on this device/i }));

    await waitFor(() => {
      expect(createPushSubscription).toHaveBeenCalledTimes(1);
    });
    const call = createPushSubscription.mock.calls[0];
    if (!call) throw new Error("createPushSubscription was not called");
    expect(new Set(call[1])).toEqual(new Set(["critical", "awaitingConfirm", "drift"]));
    expect(window.localStorage.getItem("vnprox.push.ownSubscriptionId")).toBe("push-new");
    expect(await screen.findByRole("button", { name: /disable push on this device/i })).toBeInTheDocument();
  });

  it("revoking a DIFFERENT device's subscription never touches this browser's own subscription", async () => {
    window.localStorage.setItem("vnprox.push.ownSubscriptionId", "push-mine");
    fetchPushSubscriptions.mockResolvedValueOnce([
      { id: "push-mine", categories: ["critical"], deviceLabel: "This device", createdAt: 1000 },
      { id: "push-other", categories: ["drift"], deviceLabel: "Someone's tablet", createdAt: 2000 },
    ]);
    const user = userEvent.setup();
    renderSection();

    await screen.findByText("Someone's tablet");
    const rows = screen.getAllByRole("button", { name: /revoke/i });
    // "Someone's tablet" was seeded second, so it is the second revoke button.
    const secondRevoke = rows[1];
    if (!secondRevoke) throw new Error("expected a second revoke button");
    await user.click(secondRevoke);

    await waitFor(() => {
      expect(deletePushSubscription).toHaveBeenCalledWith("push-other");
    });
    expect(unsubscribeBrowserPush).not.toHaveBeenCalled();
    expect(window.localStorage.getItem("vnprox.push.ownSubscriptionId")).toBe("push-mine");
  });

  it("revoking THIS device's own subscription also unsubscribes the browser and forgets the id", async () => {
    window.localStorage.setItem("vnprox.push.ownSubscriptionId", "push-mine");
    fetchPushSubscriptions.mockResolvedValueOnce([{ id: "push-mine", categories: ["critical"], deviceLabel: "This device", createdAt: 1000 }]);
    const user = userEvent.setup();
    renderSection();

    await screen.findByText("This device");
    await user.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() => {
      expect(deletePushSubscription).toHaveBeenCalledWith("push-mine");
    });
    expect(unsubscribeBrowserPush).toHaveBeenCalledTimes(1);
    expect(window.localStorage.getItem("vnprox.push.ownSubscriptionId")).toBeNull();
  });
});
