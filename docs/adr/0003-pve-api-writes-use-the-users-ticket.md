# ADR-0003: PVE API writes use the user's ticket, never a privileged daemon identity

**D-number:** D3 (`docs/architecture.md` §10)
**Status:** Accepted, amended (T-1805, after a real incident — see below)

> `docs/roadmap-proven.md` also has its own unrelated "D3" (that arc's decision to build a
> blocked-validation register plus a harder multi-node mock). See `docs/adr/README.md`'s
> numbering-collision table.

## Context

vnprox is an add-on to Proxmox VE, which already owns a complete auth/ACL model — realms (PAM,
PVE, LDAP/AD, OIDC), privileges (`Sys.Modify`, `Sys.Audit`, `SDN.Allocate`, `SDN.Audit`, ...), and
its own audit log. Two designs were available for how vnprox's writes reach the PVE API: authorize
every write as vnprox's own privileged service identity (the pattern many PVE plugins use), or
authorize every write as whichever human is logged in.

## Decision

Users log in with their existing Proxmox credentials; `vnproxd` forwards the login to PVE's
`POST /access/ticket`, then issues its own `HttpOnly` session cookie while holding the PVE ticket
and CSRF token server-side, renewed before the ~2h expiry. **Every PVE API write vnprox performs
uses the ticket of the user who is logged in** — never a service credential. The one privileged
internal identity vnprox holds at all, `vnprox@pve!daemon`, is **read-only** and must stay that
way (`docs/architecture.md` §6). vnprox additionally maps PVE privileges onto UI capability flags,
so a user without a privilege sees a read-only view rather than a write that fails at submit time.

## Consequences

**What this enables.** PVE's own ACL engine is the sole authority on what a write may do — vnprox
structurally cannot let a user exceed privileges they already hold in Proxmox, so there is no
privilege-escalation surface to audit for in vnprox itself. PVE's own audit log attributes every
action to the real human who performed it, not to a service account, which matters both for
compliance and for plain "who did this" debugging. It also means vnprox's UI can be honest about
what a user *can't* do (capability flags) instead of discovering it via a failed write.

**What this costs / forecloses — and the incident that proved it.** Binding every write to a
user's session ticket means a mutation cannot outlive the request or session that authorized it.
That is precisely the property that broke the commit-confirm safety net (ADR-0004) for PVE
firewall and SDN writes specifically: the change engine's unattended rollback timer fires **inside
the daemon, with no user session alive** — but a `fw.*`/`sdn.*` revert needs a PVE write
credential, and per this decision the daemon holds none of its own. `planning/reports/T-502.md`
found the resulting hole directly: a `fw.*`-only changeset that reached `awaiting_confirm` and then
timed out was **never reverted at all** — the one genuine gap in "if the change locks you out, it
reverts itself." Two fixes were rejected before the one that shipped: a standing daemon-held scoped
token (a permanent privileged credential at rest, and a revert that would act as vnprox rather than
as the user — breaking this ADR's own delegation model and PVE's audit attribution) and
confirm-only-with-a-warning (truthful, but leaving the core safety promise holed). T-1805 (tracked
as `docs/roadmap-proven.md`'s own decision "D1" — a different registry, see the numbering-collision
note above) closed the gap by **sealing the applying user's PVE ticket into the changeset row**
(AES-256-GCM, the same `SessionCipher`/session key already used for `sessions.pve_ticket_enc`,
never a second key pair) at apply time, reverting with it on the timeout and crash-recovery paths,
and wiping it the instant the changeset leaves `awaiting_confirm` by any path. This is not a second
mutation path or a walk-back of this ADR: the revert still runs as the user, through the same
rollback machinery, with a credential that window previously lacked — and coverage is explicitly
bounded by the ticket's own ~2h life and reported at apply time (`unattendedRevert` in
`docs/api.md`) rather than silently promised. The residual cost is real: vnprox now holds a new,
deliberately short-lived class of at-rest secret (`changesets.revert_ticket_enc`), reachable only
from that one changeset's own revert path (verified by registry enumeration, not convention), and
every future write-shaped feature has to reckon with the same "the daemon has no PVE write
credential of its own" constraint this decision imposes.

## See also

- `docs/architecture.md` §6 (auth model) and §4 ("What the unattended rollback can actually
  revert").
- `docs/security.md` "Apply-time revert ticket" (the full credential lifecycle).
- `internal/pve/auth.go`, `internal/change/reverticket.go`, `internal/change/apply_seams.go`.
- `planning/reports/T-502.md` (the gap), `planning/reports/T-1805.md` (the fix, and its own
  documented deviations).
