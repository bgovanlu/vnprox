# T-4307 · A renewed session cannot make a mutating request

**Status:** filed 2026-08-31 while validating the guided LLDP install · **size:** S · **depends:** — ·
**affects:** `internal/auth/renewal.go`, `internal/auth/middleware.go`, `internal/auth/handlers.go`,
`web/src/api/` (whichever module reads the `vnprox_csrf` cookie), `docs/api.md`

## The observation

CSRF is a double-submit pair: the client reads the JS-readable `vnprox_csrf` cookie and echoes it
back as `X-VNPROX-CSRF`, and `CSRFMiddleware` (`internal/auth/middleware.go:231`) compares that
header against the session row's `CSRFToken` with `subtle.ConstantTimeCompare`.

The vnprox CSRF token is not vnprox's own — it **is** PVE's `CSRFPreventionToken`, taken from the
`/access/ticket` response and stored alongside the ticket (`handlers.go:108`, `:161`).

So it changes when the ticket is renewed. The renewal loop writes the new one to the session row:

```go
ticket, csrf, err := live.identity.Renew(ctx)   // renewal.go:74
...
rec.PVETicket = ticket
rec.CSRFToken = csrf                            // renewal.go:90
```

Renewal happens on a timer, with no HTTP response in the path — and the only thing that ever sets
the cookie is `setSessionCookies`, whose sole caller is `startSession` at login
(`handlers.go:173`; `grep setSessionCookies` returns exactly the definition and that one call).

The browser therefore keeps echoing the token it was given at login, while the server has replaced
it. Every subsequent mutating request compares two different values and fails `403 csrf_required`.

## Why this reads as something else

The session is not expired, the user is not logged out, and every `GET` keeps working — reads do
not go through `CSRFMiddleware` (`middleware.go:218` treats anything other than GET/HEAD as
mutating). What the operator sees is a UI that renders fine and refuses to *do* anything, on a
session it has no reason to distrust, roughly an hour or two after logging in. "Log out and back
in" fixes it, which makes it look intermittent rather than deterministic.

Bearer-token clients are unaffected — they skip CSRF entirely by design (`middleware.go:245`) — so
`vnproxctl` and the automation contract keep working while the SPA cannot. That asymmetry is worth
keeping in mind when reproducing: an e2e suite that authenticates with a token will never see this.

The OIDC path uses a locally generated token (`oidc_handlers.go:117`) that renewal does not
rewrite, so it is also unaffected. This is specifically the PVE-password login.

## Not yet reproduced end-to-end

Everything above is read off the source and the schema. It has not been observed on pvecube — the
one live session there (created 09:20:42 today) was still inside its first ticket lifetime while
this was being written. Reproducing it is the first acceptance criterion rather than a
prerequisite, because a fix that lands without a failing test first would be indistinguishable
from the several gates this phase has already caught measuring the wrong thing.

Note that `TicketRenewCheckInterval` defaults to one minute (`service.go:160`) — that is the
*check* cadence, not the renew cadence, so a reproduction has to drive the actual renew trigger
rather than wait a minute.

## Acceptance criteria

1. A test that logs in, forces one ticket renewal, and then makes a mutating request with the
   cookie value the client was originally handed — and fails `403` before the fix. Without this
   ordering the card proves nothing.
2. The client's `vnprox_csrf` cookie and the stored `CSRFToken` agree after a renewal. The obvious
   shape is for `SessionMiddleware` to re-set the cookie when it notices the stored token differs
   from the presented one; decide explicitly against the alternative (decoupling vnprox's CSRF
   token from PVE's, as the OIDC path already does) and record why in the commit.
3. Whatever the chosen shape, a `GET` must not be able to rotate a token that a concurrent
   in-flight mutating request is about to present — otherwise the fix trades a deterministic
   failure for a racy one.
4. `docs/api.md`'s CSRF description says what a client must do when the token rotates mid-session.

## Also noticed in the same read, not part of this card

`clientIP` uses `RemoteAddr` and deliberately ignores forwarded headers (`handlers.go:63`). That is
the right default, but it means login rate limiting collapses to a single bucket behind a reverse
proxy. If that is intended, it deserves a sentence in `docs/security.md`; if not, it is its own
card.
