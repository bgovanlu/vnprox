import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PresenceIndicator } from "./PresenceIndicator";
import type { PresenceResponse } from "../api/types";

const getPresence = vi.fn();
const subscribe = vi.fn(() => () => undefined);

vi.mock("../api/locks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/locks")>();
  return { ...actual, getPresence: (scope?: string) => getPresence(scope) as Promise<PresenceResponse> };
});

vi.mock("../api/ws", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/ws")>();
  return { ...actual, createWsClient: () => ({ subscribe, status: () => "open", close: () => undefined }) };
});

async function renderIndicator(response: PresenceResponse, currentUser?: string) {
  getPresence.mockResolvedValue(response);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={queryClient}>
      <PresenceIndicator changesetId="cs-1" currentUser={currentUser} />
    </QueryClientProvider>,
  );
  // Let the query settle by waiting for the thing that actually settles it —
  // the presence fetch resolving — rather than by awaiting a `findBy` that is
  // *expected* to reject and swallowing it.
  //
  // The old idiom (`await screen.findByTestId(...).catch(() => undefined)`)
  // worked, but it paid Testing Library's full async timeout on every call,
  // by construction: two of these cases assert the indicator is ABSENT, so
  // the find can only ever time out. That made the helper's cost equal to
  // whatever `asyncUtilTimeout` happens to be — invisible at the 1000ms
  // default, and an instant failure when the shared setup raised it to
  // 5000ms and collided with vitest's own 5000ms testTimeout.
  await waitFor(() => {
    expect(getPresence).toHaveBeenCalled();
  });
  return view;
}

describe("PresenceIndicator", () => {
  it("says nothing when this operator is the only one here", async () => {
    await renderIndicator(
      { scopes: [{ scope: "changeset:cs-1", count: 1, viewers: [{ user: "alice@pam", since: 1, sessions: 1 }] }] },
      "alice@pam",
    );
    expect(screen.queryByTestId("presence-indicator")).not.toBeInTheDocument();
  });

  it("names the other people viewing the changeset", async () => {
    await renderIndicator(
      {
        scopes: [
          {
            scope: "changeset:cs-1",
            count: 2,
            viewers: [
              { user: "alice@pam", since: 1, sessions: 1 },
              { user: "bob@pam", since: 2, sessions: 1 },
            ],
          },
        ],
      },
      "alice@pam",
    );
    expect(await screen.findByTestId("presence-indicator")).toHaveTextContent("bob@pam is also viewing this.");
  });

  // T-2805 AC5's UI consequence: with `viewers` withheld the count still
  // arrives, and a count is not an identity.
  it("states the count alone when identities are withheld", async () => {
    await renderIndicator({ scopes: [{ scope: "changeset:cs-1", count: 3 }] }, "alice@pam");
    expect(await screen.findByTestId("presence-indicator")).toHaveTextContent("2 other people are viewing this.");
  });

  it("subscribes to this changeset's presence topic — the subscription IS the declaration", async () => {
    await renderIndicator({ scopes: [{ scope: "changeset:cs-1", count: 1 }] }, "alice@pam");
    expect(subscribe).toHaveBeenCalledWith(["presence:changeset:cs-1"], expect.any(Function));
  });
});
