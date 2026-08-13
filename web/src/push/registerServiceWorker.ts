// Registers web/public/sw.js — called once from main.tsx. A no-op (and no
// thrown error) in a browser without service worker support, or in a test/
// SSR environment with no `navigator`.
export function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;

  window.addEventListener("load", () => {
    void navigator.serviceWorker.register("/sw.js").catch((err: unknown) => {
      // Best-effort: a failed registration degrades to "no push, no
      // offline shell", not a broken app — every route still works
      // online exactly as it did before this task.
      console.warn("vnprox: service worker registration failed", err);
    });
  });
}
