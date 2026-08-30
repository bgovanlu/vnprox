// SPDX-License-Identifier: Apache-2.0

// T-4210: the routed-page inventory the visual regression suite (visual.spec.ts)
// crawls, derived directly from App.tsx's <Route> declarations rather than
// hand-copied.
//
// WHY SOURCE-DERIVED AND NOT A SECOND COPY OF a11y.spec.ts's SWEEP_ROUTES.
// The task this file was written for ("reuse that enumeration approach rather
// than inventing a second list that can drift") points at SWEEP_ROUTES, but
// that table is a private, unexported const with no seam to import through —
// and it has already drifted once: as of this writing it is missing "/guest"
// (GuestEgoPage, T-3906) and "/wireguard" (WireGuardPage, T-4015), both real
// routes App.tsx declares. Copying it would import the drift along with the
// list. Re-deriving from App.tsx is the *technique* SWEEP_ROUTES was built
// from in the first place, and the same one web/src/help/coverage.test.ts's
// route inventory already leans on for the help-topic coverage gate ("the
// screen inventory... derived from the shipped source, never hand-maintained"
// — see that file's own header comment). A source-derived list cannot drift
// from the router; a second hand-list can, the same way the first one did.
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** <Route path="..."> extraction, mirroring
 * web/src/help/coverage.test.ts's own `<Route[^>]*?\spath="([^"]+)"` pattern
 * so the two source-derived inventories cannot disagree about what counts as
 * a declared route. */
const ROUTE_PATH_RE = /<Route[^>]*?\spath="([^"]+)"/g;

/** Route paths this suite deliberately does not crawl, each for a reason
 * that is about the ROUTE, not about coverage laziness. */
const EXCLUDED_EXACT = new Set<string>([
  // The catch-all redirect to /topology — not a screen of its own, and
  // navigating to it would just screenshot /topology a second time under a
  // path that means nothing.
  "*",
  // Pre-auth chrome: every other route in this inventory needs the logged-in
  // AppShell (RequireAuth wraps them all in App.tsx), so /login gets its own
  // dedicated visual.spec.ts test outside this table rather than a row that
  // would need a completely different (unauthenticated) setup.
  "/login",
]);

/** Every routed page's path, in the logged-in AppShell that this suite's
 * login-once storage state actually reaches.
 *
 * Excluded, beyond EXCLUDED_EXACT above:
 *  - The three /embed/* routes (T-1706): token-authenticated, chrome-less
 *    embeds outside RequireAuth/AppShell entirely — a different auth model
 *    and a different layout, out of a "routed app-shell page" sweep's scope.
 *  - Any path with a `:param` segment (currently only
 *    /changesets/:id/review): this is a static page crawl, not a workflow
 *    driver. A screenshot of that route needs a real changeset id to render
 *    anything meaningful, which belongs in a dedicated, purpose-built test —
 *    not a table that assumes "navigate and capture" is enough.
 *  - "/" itself is added back explicitly below: App.tsx's landing route is
 *    `<Route index element={<DashboardPage />} />`, which carries no
 *    `path="..."` attribute for the regex above to find.
 */
export function routedPagePaths(): string[] {
  const source = readFileSync(join(REPO_ROOT, "web", "src", "App.tsx"), "utf8");
  const found = new Set<string>(["/"]);
  for (const m of source.matchAll(ROUTE_PATH_RE)) {
    const routePath = m[1];
    if (routePath === undefined) continue;
    if (EXCLUDED_EXACT.has(routePath)) continue;
    if (routePath.startsWith("/embed/")) continue;
    if (routePath.includes(":")) continue;
    found.add(routePath);
  }

  const paths = [...found].sort();
  // The same "the scan is probably broken, not the repo" guard
  // TestScanFindsKnownPorts (internal/devports) and coverage.test.ts's own
  // route-count assertion both use: a regex that stops matching should fail
  // loudly, not quietly shrink the suite to a handful of pages.
  if (paths.length < 20) {
    throw new Error(
      `routedPagePaths() found only ${String(paths.length)} route(s) in App.tsx (${paths.join(", ")}) — ` +
        "the extraction regex has probably stopped matching, not the app lost most of its screens.",
    );
  }
  return paths;
}

/** Turns a route path into a filesystem/snapshot-name-safe slug.
 * "/" -> "home"; "/settings/alert-rules" -> "settings-alert-rules". */
export function slugifyRoute(routePath: string): string {
  if (routePath === "/") return "home";
  return routePath.replace(/^\//, "").replace(/\//g, "-");
}

/** Heading level overrides for the generic "did the app actually navigate"
 * assertion in visual.spec.ts. Every routed page renders a single level-1
 * PageHeader heading (T-3404) EXCEPT DiagnosePage: with no `?ref=` query
 * param (a plain nav-rail hit or bookmark, exactly what this crawl does) it
 * renders only its EmptyState's level-2 "No target selected" heading and
 * never mounts PageHeader/<h1> at all — the same real exception
 * a11y.spec.ts's SWEEP_ROUTES documents for the same route. Kept as a tiny,
 * explicit map (one entry) rather than a second per-route heading-text table:
 * this only overrides the *level* the generic check looks for, not a second
 * copy of route->heading knowledge that could drift from App.tsx.
 */
export const HEADING_LEVEL_OVERRIDES: Readonly<Record<string, 1 | 2>> = {
  "/diagnose": 2,
};

/** A `toHaveURL` matcher for a route path: the path must END the URL, except
 * for a query string or fragment after it.
 *
 * Anchored so "/guests" cannot satisfy a check for "/guest". The `[?#]` tail
 * is not decoration — several pages rewrite their own URL after mount to
 * record state (`/firewall/compiled` redirects to `?node=pve1`), and a bare
 * `$` anchor turns that into a RACE: the assertion passes if it runs before
 * the rewrite and fails if it runs after. It passed 102/102 twice and then
 * failed once, which is the worst way for a gate to be wrong — found only
 * because T-4213's injected-regression run happened to lose the race.
 *
 * Lives here rather than in either spec because it is route knowledge, and
 * T-4212 exists because route knowledge kept in two places drifts. Both the
 * visual gate and the axe sweep assert the URL as their "did we actually land
 * on the page we asked for" check — neither needs a per-route heading table to
 * do it. */
export function pathEndRegExp(literal: string): RegExp {
  return new RegExp(`${literal.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:[?#].*)?$`);
}
