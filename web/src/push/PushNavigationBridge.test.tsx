// SPDX-License-Identifier: Apache-2.0

// PushNavigationBridge relays web/public/sw.js's postMessage into an
// in-app navigation. jsdom has no real ServiceWorkerContainer, so this
// test stubs `navigator.serviceWorker` as a plain EventTarget — enough to
// drive the component's addEventListener("message", ...) subscription and
// dispatch a synthetic MessageEvent exactly like a browser would deliver
// one from the service worker.
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { PushNavigationBridge } from "./PushNavigationBridge";

// Reassigned fresh before every test (never torn down afterward) — RTL's
// own automatic unmount-on-cleanup runs in ITS afterEach, which reads
// navigator.serviceWorker during PushNavigationBridge's effect-cleanup;
// tearing this stub down first would make that cleanup crash on
// `undefined.removeEventListener`, an artifact of test teardown ordering
// rather than anything about the component itself.
let fakeServiceWorker: EventTarget;

beforeEach(() => {
  fakeServiceWorker = new EventTarget();
  Object.defineProperty(navigator, "serviceWorker", {
    value: fakeServiceWorker,
    configurable: true,
  });
});

function renderBridge(): void {
  render(
    <MemoryRouter initialEntries={["/"]}>
      <PushNavigationBridge />
      <Routes>
        <Route path="/" element={<div>home page</div>} />
        <Route path="/changesets/:id/review" element={<div>review page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PushNavigationBridge", () => {
  it("navigates the app when the service worker posts a vnprox-push-navigate message", async () => {
    renderBridge();
    expect(screen.getByText("home page")).toBeInTheDocument();

    fakeServiceWorker.dispatchEvent(
      new MessageEvent("message", { data: { type: "vnprox-push-navigate", url: "/changesets/cs-1/review" } }),
    );

    expect(await screen.findByText("review page")).toBeInTheDocument();
  });

  it("ignores a message of an unrelated shape, EVEN ONE THAT CARRIES A VALID-LOOKING url", async () => {
    // The url alone matching this app's own route shape is deliberately
    // not enough to trigger a navigation — only `type ===
    // "vnprox-push-navigate"` should. A guard that checks url's presence/
    // shape but forgets to check `type` would still pass a weaker version
    // of this assertion (nothing navigates when there's no url at all);
    // this is the version that actually exercises the type discriminator.
    renderBridge();
    fakeServiceWorker.dispatchEvent(
      new MessageEvent("message", { data: { type: "something-else", url: "/changesets/cs-1/review" } }),
    );
    // Give any (incorrect) navigation a tick to have happened before
    // asserting it didn't.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.getByText("home page")).toBeInTheDocument();
    expect(screen.queryByText("review page")).not.toBeInTheDocument();
  });

  it("ignores a message with no data at all", () => {
    renderBridge();
    fakeServiceWorker.dispatchEvent(new MessageEvent("message"));
    expect(screen.getByText("home page")).toBeInTheDocument();
  });
});
