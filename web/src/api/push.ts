// SPDX-License-Identifier: Apache-2.0

// Web-push API calls against internal/api/push.go (T-2005): the VAPID
// public key a browser's `PushManager.subscribe()` needs, and CRUD over
// this session's own registered push subscriptions. Mirrors annotations.ts'
// shape (apiFetch + readCsrfCookie on every mutating call).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";

/** The three categories internal/push.AllCategories documents — kept in
 * sync by inspection (a category the server doesn't recognize is rejected
 * with a 400 by POST /push/subscriptions, so a drift here surfaces as a
 * visible error rather than silently). */
export type PushCategory = "critical" | "awaitingConfirm" | "drift";

export const ALL_PUSH_CATEGORIES: PushCategory[] = ["critical", "awaitingConfirm", "drift"];

export interface PushSubscriptionSummary {
  id: string;
  categories: PushCategory[];
  deviceLabel?: string;
  createdAt: number;
  lastUsedAt?: number;
}

interface PushSubscriptionsListResponse {
  items: PushSubscriptionSummary[];
}

interface VapidPublicKeyResponse {
  key: string;
}

/** GET /push/vapid-public-key — the `applicationServerKey` this browser's
 * `pushManager.subscribe()` call needs. */
export async function fetchVapidPublicKey(): Promise<string> {
  const res = await apiFetch<VapidPublicKeyResponse>("/push/vapid-public-key");
  return res.key;
}

/** GET /push/subscriptions — this session's own devices, newest first. */
export function fetchPushSubscriptions(): Promise<PushSubscriptionSummary[]> {
  return apiFetch<PushSubscriptionsListResponse>("/push/subscriptions").then((res) => res.items);
}

/** POST /push/subscriptions — registers subscription (the browser's
 * `PushSubscription.toJSON()` shape) for categories, labeled deviceLabel
 * for display. Never echoes the endpoint/keys back — see
 * pushSubscriptionResponse's doc comment in internal/api/push.go. */
export function createPushSubscription(
  subscription: PushSubscriptionJSON,
  categories: PushCategory[],
  deviceLabel?: string,
): Promise<PushSubscriptionSummary> {
  return apiFetch<PushSubscriptionSummary>("/push/subscriptions", {
    method: "POST",
    json: {
      endpoint: subscription.endpoint,
      keys: subscription.keys,
      categories,
      deviceLabel,
    },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /push/subscriptions/{id} — revokes one of the caller's own
 * devices; idempotent from the caller's point of view (a 404 on an
 * already-gone id is not surfaced as a failure by callers that just want
 * "make sure this is gone"). */
export async function deletePushSubscription(id: string): Promise<void> {
  await apiFetch(`/push/subscriptions/${encodeURIComponent(id)}`, {
    method: "DELETE",
    csrfToken: readCsrfCookie(),
  });
}

/** The subset of the DOM's PushSubscriptionJSON this app actually reads —
 * declared locally (rather than relying on lib.dom's own type, which marks
 * every field optional to match the spec precisely) so callers get a
 * compile error if they pass something that hasn't actually been
 * subscribed yet. */
export interface PushSubscriptionJSON {
  endpoint: string;
  keys: {
    p256dh: string;
    auth: string;
  };
}
