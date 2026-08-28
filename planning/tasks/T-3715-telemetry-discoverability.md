# T-3715 · Telemetry is off by default and effectively undiscoverable

**Found by:** T-3812's consent-UX review, 2026-08-27 · **size:** S · **depends:** — ·
**affects:** T-2503 (telemetry client), T-3710 (collector), T-3812 (transparency page)

## The observation

Telemetry's *safety* properties are strong and were verified in code, not assumed: structurally
off by default (no default endpoint exists in `internal/config`; `ValidateTelemetry` requires an
explicit `https://` endpoint before `enabled=true` means anything), `vnproxctl telemetry preview`
prints the exact bytes that would be sent, the collector never reads `r.RemoteAddr` (nor
`X-Forwarded-For`/`X-Real-IP`), rate limiting keys on `installId`, retention is a real `DELETE`,
and revoke is idempotent (`deleted 1`, then `deleted 0`).

**The gap is the opposite of the usual one.** There is no first-run notice, no installer prompt
(verified absent from `packaging/debian/postinst` and `packaging/build/pkgroot/DEBIAN/postinst`),
and no mention anywhere in the web UI (`grep -rl telemetry web/src` finds nothing). An operator
can only discover the feature by already knowing to read `vnprox.toml`'s comments or by finding
`docs/telemetry.md`.

## Why this is worth a card rather than a shrug

The project asked for telemetry so it could learn what real deployments look like. A consent
mechanism nobody can find collects nothing, so the feature is inert in practice for the same
reason it is safe — and "inert" is exactly the classification Phase 37 spent a wave clearing out.
It is also the honest reading of the opt-in promise: opt-in means the operator got a real chance
to say yes, not that saying yes was possible in principle.

## Deliverables

- A one-time, **non-interactive** notice from `vnproxctl verify` (or an equally low-traffic
  operator command) pointing at `vnproxctl telemetry status`. Non-interactive matters: this must
  not become a prompt that blocks automation or an install.
- Decide, and record, whether the web UI gets a mention. A settings-page line is the obvious
  candidate; a modal is not — it would be the dark-pattern version of fixing this.
- **Do not change the default.** Off-by-default is correct and is not what this card is about.
  Any change that makes telemetry on-by-default, or that makes the notice hard to dismiss, is a
  failure of this card, not a fulfilment of it.

## Acceptance criteria

1. A fresh install surfaces the existence of telemetry exactly once, through a path an operator
   actually traverses, without prompting or blocking.
2. `vnprox.toml`'s default remains `enabled = false`, and a test asserts it.
3. `docs/telemetry.md` documents where the notice appears, so the next reviewer can check the
   claim instead of re-deriving it.
