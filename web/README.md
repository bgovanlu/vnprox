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
  layout/     AppShell (nav rail + top bar + routed outlet), theme toggle
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

Real Proxmox-credential auth lands in T-105. Until then, `GET /auth/me` 404s against the
current backend stub, which the session bootstrap (`src/api/useSession.ts`) treats the
same as "logged out" — and the login page offers a demo-mode bypass
(`src/store/authStub.ts`, `VITE_AUTH_STUB=false` to disable) so the shell is still
demoable. See that file and `src/routes/RequireAuth.tsx` for the exact contract.

## Testing

`npm test` runs Vitest once (`npm run test:watch` to watch). Notable suites:

- `src/api/client.test.ts` — the error-envelope-normalizing fetch wrapper.
- `src/api/ws.test.ts` — the WS client's reconnect/backoff and resubscribe behavior,
  against a real `ws` server that's killed and restarted mid-test.
- `src/store/theme.test.ts` — the dark/light theme persisting across a simulated reload.
