// SPDX-License-Identifier: Apache-2.0

import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup, configure } from "@testing-library/react";

// Testing Library's `findBy*` default is a 1000ms timeout, which is a
// statement about how long a render should take on an idle machine. This
// suite does not run on an idle machine: the pre-push hook is `make ci`,
// which runs vitest alongside a cross-arm64 build, a fuzz corpus run and a
// package build, and `scripts/ci-local.sh` is heavier still.
//
// The failure that prompted this is worth recording, because it is the
// shape the whole class takes. TenantsPanel.test.tsx timed out on
// `findByRole("button", {name: /Team Blue/})` during a push, refusing it;
// the same file then passed 3/3 in isolation and the full 2278-test suite
// passed 295/295 immediately after. Nothing was broken — a react-query
// resolve plus a render simply took longer than a second under load, and a
// green suite was reported as a red one.
//
// 5s is chosen to be far outside the load-induced range while still being
// short enough that a genuinely stuck query fails the test rather than
// hanging it. It does not slow a passing test down: `findBy*` polls and
// resolves as soon as the element appears, so the timeout is a ceiling, not
// a wait.
configure({ asyncUtilTimeout: 5000 });

afterEach(() => {
  cleanup();
});

// jsdom has no ResizeObserver (it doesn't lay out or paint anything), but
// @xyflow/react's canvas (src/topology/TopologyCanvas.tsx) requires one to
// mount at all — it watches its container for size changes. A minimal
// no-op stub is the standard fix for testing React Flow components under
// jsdom; real browsers always have the real implementation.
class ResizeObserverStub {
  observe(): void {
    /* no-op */
  }
  unobserve(): void {
    /* no-op */
  }
  disconnect(): void {
    /* no-op */
  }
}
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = ResizeObserverStub;
}
