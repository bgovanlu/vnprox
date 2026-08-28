// SPDX-License-Identifier: Apache-2.0

// This device's own push_subscriptions row id, remembered locally so
// "disable push on this device" (PushSettingsSection.tsx) knows which
// server-side row to DELETE without the server ever having to echo back
// anything endpoint-derived (it deliberately never does — see
// pushSubscriptionResponse's doc comment in internal/api/push.go). Plain
// localStorage, not app state: this is a fact about THIS BROWSER, not
// about the logged-in session (T-909-style "client preference" data, the
// same tier Settings' theme choice lives in), and it must survive a page
// reload without a round trip.
const STORAGE_KEY = "vnprox.push.ownSubscriptionId";

export function getOwnSubscriptionId(): string | undefined {
  try {
    return window.localStorage.getItem(STORAGE_KEY) ?? undefined;
  } catch {
    // Storage disabled (private browsing in some browsers, or a
    // locked-down profile) — degrade to "no known id", same as a fresh
    // browser profile.
    return undefined;
  }
}

export function setOwnSubscriptionId(id: string): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // Best-effort; a failed write just means the next visit won't
    // remember this device subscribed, which is a UX nit, not a
    // correctness bug — the server row still exists and is still
    // revocable from the "Your devices" list by anyone who can see it.
  }
}

export function clearOwnSubscriptionId(): void {
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // See setOwnSubscriptionId.
  }
}
