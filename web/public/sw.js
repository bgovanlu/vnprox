// T-2005's service worker: installability (a fetch handler is required for
// Chromium's installability check), a best-effort offline app shell, and
// web-push delivery (RFC 8291 decryption happens in the browser itself —
// this file only ever sees the plaintext internal/push.Notification the
// daemon decided to send, per that package's closed-schema payload.go).
//
// SECURITY (docs/security.md, this task's card): this cache is NEVER used
// for `/api/*` requests. vnprox is a single-origin app where every
// authenticated response comes from the SAME origin this service worker
// controls, and a shared device (a kiosk, a family member's laptop) could
// have a second vnprox session log in after this one — a cached API
// response would leak the FIRST user's data to the SECOND. Every fetch
// handler below checks this first and, for `/api/*`, does not even touch
// the cache: the request goes straight to the network, uncached, exactly
// as if this service worker did not exist. Static build assets (JS/CSS/
// icons, content-hashed by Vite) and the app shell HTML are the only
// things ever cached — never a response carrying this session's data.
const CACHE_NAME = "vnprox-shell-v1";

// APP_SHELL_URLS is the fixed, version-independent set of URLs precached at
// install — deliberately small and deliberately NOT a hashed asset
// manifest (this project's dependency policy avoids taking on a build
// plugin like vite-plugin-pwa just to generate one — see the task report).
// Hashed JS/CSS chunks are instead cached opportunistically as the app
// fetches them (see the fetch handler's runtime-caching branch below), so
// the offline shell available after a first visit grows to cover whatever
// that visit actually loaded, not just this fixed list.
const APP_SHELL_URLS = ["/", "/manifest.webmanifest", "/icons/icon-192.png", "/icons/icon-512.png"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      .then((cache) => cache.addAll(APP_SHELL_URLS))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n !== CACHE_NAME).map((n) => caches.delete(n))))
      .then(() => self.clients.claim()),
  );
});

// isAPIRequest is the one check every branch below defers to before ever
// touching the cache — see the file-level SECURITY comment.
function isAPIRequest(url) {
  return url.pathname.startsWith("/api/");
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return; // never cache a mutation, ever
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // same-origin only
  if (isAPIRequest(url)) return; // SECURITY: never intercepted, never cached

  // Network-first, falling back to whatever shell was cached on a prior
  // visit: an online reader always gets the current build; an offline one
  // gets the last build this device actually saw, which is what makes the
  // app open at all with no connection rather than showing the browser's
  // own offline error page.
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (res.ok) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
        }
        return res;
      })
      .catch(() => caches.match(req).then((cached) => cached || caches.match("/"))),
  );
});

// --- Web push (T-2005) -----------------------------------------------------
//
// The payload is EXACTLY internal/push.Notification's marshaled JSON
// (payload.go): {category, event, title, body, url}. This handler renders
// ONLY `title` and `body` as the visible notification text, and passes
// `url` through as opaque `data` used solely to build the deep link on
// click — never displayed. This split matters because it is what "must not
// leak the network's shape to a phone lock screen" (the task's security
// note) actually means in code: internal/push guarantees title/body never
// contain a hostname/guest name/IP, so rendering exactly those two fields
// and nothing else is what keeps that guarantee intact on the client side
// too — this file has no logic that could reintroduce a leak by, say,
// stringifying the whole payload into the body.
self.addEventListener("push", (event) => {
  if (!event.data) return;
  let payload;
  try {
    payload = event.data.json();
  } catch {
    return; // not a payload this app sent — ignore rather than guess
  }
  const title = typeof payload.title === "string" ? payload.title : "vnprox";
  const body = typeof payload.body === "string" ? payload.body : "";
  const url = typeof payload.url === "string" ? payload.url : "/";
  const category = typeof payload.category === "string" ? payload.category : "vnprox";

  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      icon: "/icons/icon-192.png",
      badge: "/icons/icon-192.png",
      // tag+renotify: a second "drift detected" push replaces the first
      // rather than stacking a dozen near-identical notifications — the
      // reader only ever needs the latest state per category.
      tag: category,
      renotify: true,
      data: { url },
    }),
  );
});

// notificationclick focuses an already-open vnprox tab and navigates it,
// or opens a new one — either way landing on `url`, an app-relative path
// (never absolute — this app is served from exactly one origin) that the
// app's own router + RequireAuth guard take it from there. This is ONLY
// ever a navigation: it opens a URL, nothing more. Confirming a changeset
// or acknowledging a finding still requires the app's own authenticated
// session and capability check on the page the click lands on — this
// handler cannot act on the user's behalf and does not try to (T-2005's
// safety note: "the notification is a deep link, never an action token").
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || "/";
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if ("focus" in client) {
          client.postMessage({ type: "vnprox-push-navigate", url });
          return client.focus();
        }
      }
      return self.clients.openWindow(url);
    }),
  );
});
