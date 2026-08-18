# web/

The vnprox SPA: React 18 + TypeScript (strict) + Vite. Built by `make build`/`make dev`
(see the repo-root `Makefile`); this directory's own scripts also work standalone from
here (`npm run dev`, `npm run build`, `npm test`, `npm run lint`).

## Layout

```
src/
  api/        fetch wrapper (client.ts), WS client (ws.ts), auth calls, wire types
  components/ shared UI: Button, Dialog, Drawer, Table, Toast, EmptyState
  keyboard/   the docs/user-guide.md §6 shortcut framework + `?` help dialog
  layout/     AppShell (Sidebar + top bar + routed outlet), theme toggle
  lib/        TanStack Query client configuration
  pages/      one component per top-level route (mostly placeholders so far)
  routes/     RequireAuth session guard
  store/      zustand stores (theme, demo-mode auth stub)
  test/       Vitest setup (jsdom, Testing Library, jest-dom matchers)
```

## Conventions

- `strict: true` TypeScript, `noUncheckedIndexedAccess: true` — see the root `tsconfig.json`
  (this directory's `tsconfig.json` extends it) and `docs/development.md`'s TypeScript
  standards section.
- No `fetch` in components — all server state goes through TanStack Query, using
  `api/client.ts`'s `apiFetch` (which normalizes docs/api.md's
  `{"error":{"code","message","details"}}` envelope into a single `ApiError` type).
- Wire types live in `src/api/types.ts`, hand-maintained to mirror `docs/api.md` — add to
  that file rather than declaring API shapes elsewhere.
- Function components only; canvas/interaction state that isn't server state goes in
  zustand (`src/store/`).

## Auth today

Real Proxmox-credential auth landed in T-105: the login page POSTs `/auth/login` and the
session bootstrap (`src/api/useSession.ts`) drives `GET /auth/me`. Under `make dev`
(vnproxd + pvemock) log in with the mock fixture's users — e.g. `root` / `vnprox-mock`,
realm `pam` (see `testdata/clusters/*.yaml` for the other personas).

The old demo-mode bypass (`src/store/authStub.ts`) still exists for demoing the SPA with
no backend at all, but is **off by default** — set `VITE_AUTH_STUB=true` (e.g.
`VITE_AUTH_STUB=true npm run dev`) to show it. It is client-side only: the backend still
401s every API call without a real session. See that file and
`src/routes/RequireAuth.tsx` for the exact contract.

## Testing

`npm test` runs Vitest once (`npm run test:watch` to watch). Notable suites:

- `src/api/client.test.ts` — the error-envelope-normalizing fetch wrapper.
- `src/api/ws.test.ts` — the WS client's reconnect/backoff and resubscribe behavior,
  against a real `ws` server that's killed and restarted mid-test.
- `src/store/theme.test.ts` — the dark/light theme persisting across a simulated reload.

Opt-in Playwright e2e (`npm run e2e` — real pvemock + vnproxd stack, real login,
screenshot baseline, pan/zoom frame timing) lives in `e2e/`; it is deliberately not part
of `npm test`/`make check`. See `docs/testing/topology-render-verification.md`.
