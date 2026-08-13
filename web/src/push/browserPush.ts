// browserPush.ts wraps the browser's native Push API (no client library —
// this is a handful of calls against `navigator.serviceWorker`/
// `PushManager`, the same "stdlib first" preference
// docs/development.md states for the Go side, applied here since there is
// nothing a dependency would add beyond what these ~40 lines already do).

/** True if this browser can register a service worker and hold a push
 * subscription at all — Safari on older iOS, and any non-HTTPS/non-
 * localhost origin, notably cannot. Every entry point below checks this
 * first and degrades to an explanatory disabled state rather than throwing. */
export function isPushSupported(): boolean {
  return typeof navigator !== "undefined" && "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

/** The three states this app ever shows the user: browser support,
 * Notification permission, and whether an active PushManager subscription
 * exists right now (independent of what internal/api's store thinks — see
 * usePushSubscriptionStatus's doc comment on why both are checked). */
export interface PushBrowserStatus {
  supported: boolean;
  permission: NotificationPermission | "unsupported";
  subscription: PushSubscription | null;
}

export async function getPushBrowserStatus(): Promise<PushBrowserStatus> {
  if (!isPushSupported()) {
    return { supported: false, permission: "unsupported", subscription: null };
  }
  const registration = await navigator.serviceWorker.ready;
  const subscription = await registration.pushManager.getSubscription();
  return { supported: true, permission: Notification.permission, subscription };
}

/** urlBase64ToUint8Array converts the VAPID public key GET
 * /push/vapid-public-key returns (base64url, unpadded —
 * internal/push.PublicKeyBase64URL's doc comment) into the raw
 * `Uint8Array` `PushManager.subscribe`'s `applicationServerKey` option
 * requires. Padding is restored (`=` to a multiple of 4) since `atob`
 * requires standard padded base64, then base64url's `-`/`_` are mapped
 * back to standard `+`/`/`. */
function urlBase64ToUint8Array(base64url: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64url.length % 4)) % 4);
  const base64 = (base64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  // Constructed from an explicit ArrayBuffer (not just a length) so this
  // is typed Uint8Array<ArrayBuffer> rather than the newer TS DOM lib's
  // default Uint8Array<ArrayBufferLike> — PushManager.subscribe's
  // applicationServerKey option requires the narrower type.
  const buffer = new ArrayBuffer(raw.length);
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < raw.length; i++) {
    bytes[i] = raw.charCodeAt(i);
  }
  return bytes;
}

/** requestPushSubscription asks the browser for notification permission
 * (a user-gesture-gated prompt — must be called from a click handler, not
 * on mount) and, if granted, subscribes via the service worker's
 * PushManager using vapidPublicKey. Returns null if permission was denied
 * or the browser doesn't support push at all; throws only for a genuine
 * subscribe failure (network error, malformed key) so callers can
 * distinguish "the user said no" from "something broke". */
export async function requestPushSubscription(vapidPublicKey: string): Promise<PushSubscription | null> {
  if (!isPushSupported()) return null;

  const permission = await Notification.requestPermission();
  if (permission !== "granted") return null;

  const registration = await navigator.serviceWorker.ready;
  const existing = await registration.pushManager.getSubscription();
  if (existing) return existing;

  return registration.pushManager.subscribe({
    userVisibleOnly: true, // required by every browser that implements this API
    applicationServerKey: urlBase64ToUint8Array(vapidPublicKey),
  });
}

/** unsubscribeBrowserPush tears down the browser-side PushManager
 * subscription. Callers are responsible for also calling DELETE
 * /push/subscriptions/{id} (api/push.ts) — the two are independent halves
 * (browser state vs. server record) and either can outlive the other if a
 * call fails partway, which is why usePushSubscription's mutation does both
 * and reports either half's failure. */
export async function unsubscribeBrowserPush(): Promise<boolean> {
  if (!isPushSupported()) return false;
  const registration = await navigator.serviceWorker.ready;
  const existing = await registration.pushManager.getSubscription();
  if (!existing) return true;
  return existing.unsubscribe();
}
