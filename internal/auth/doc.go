// Package auth implements vnprox's PVE credential bridge (T-105):
//
//   - POST /auth/login, POST /auth/logout, GET /auth/me (docs/api.md),
//     login rate limiting (per-IP + per-username token buckets), and
//     login/logout/lockout audit entries (internal/store's AuditRepo).
//   - Server-side sessions (internal/store's SessionRepo/SessionCipher):
//     random 256-bit id in an HttpOnly+Secure+SameSite=Strict cookie
//     (vnprox_session), PVE ticket + CSRF token encrypted at rest, idle
//     (2h) + hard (12h) expiry, background ticket renewal (~1h30, reusing
//     internal/pve's own renewal mechanism).
//   - CSRF: double-submit via a JS-readable vnprox_csrf cookie the client
//     echoes back as X-VNPROX-CSRF on mutating requests.
//   - Capability derivation (caps.go): PVE ACL privileges ->
//     {netRead, netWrite, sdnRead, sdnWrite, fwRead, fwWrite, guestNet,
//     audit} per node, re-derived hourly. This is vnprox's own
//     ("vnprox-enforced") authorization layer for host-level operations
//     that bypass the PVE API; the primary layer is always PVE's own ACL
//     check on the logged-in user's own ticket (docs/security.md
//     "Authorization").
//   - Middleware other route registrations build on: SessionMiddleware ->
//     CSRFMiddleware -> RequireCap(cap) (middleware.go).
//
// # pvemock fidelity gaps
//
// internal/pvemock (T-004) does not implement two things real PVE has that
// this package's production code depends on, and this package was
// instructed not to modify pvemock to add them:
//
//   - GET /access/permissions (capability derivation's data source): the
//     mock's fixture-defined UserSpec.Privileges is a single flat,
//     non-path-scoped list checked via session.hasPrivilege — there is no
//     "/access/permissions" route in internal/pvemock's router at all.
//     pve.Client.Permissions (added by this task, internal/pve/permissions.go)
//     is implemented against the documented real-PVE contract regardless,
//     so it works unmodified once pointed at real PVE (or a future mock
//     update). This package's integration tests substitute a PVEIdentity
//     decorator whose Permissions method reads the SAME fixture data the
//     live mock server loaded (via pvemock.LoadFixture, already exported)
//     and reshapes it into a pve.Permissions value — not fabricated mock
//     behavior, just working around an HTTP endpoint that doesn't exist yet.
//     Login, ticket renewal, and node enumeration (GET /cluster/status) in
//     those same tests still go out over real HTTP to the mock.
//   - TOTP/second-factor: internal/pvemock's handleTicket never inspects an
//     "otp" field, and no fixture user requires one. This package's OTP
//     passthrough (the "otp" field on POST /auth/login is forwarded
//     verbatim to pve.Config.OTP, which internal/pve's ticketAuth sends
//     through to PVE's own POST /access/ticket on first login only) is
//     exercised by a handler-level unit test using a stub PVEIdentity that
//     requires a specific OTP value, standing in for a PVE realm with a
//     second factor configured — the task card explicitly sanctions this
//     ("a unit test on the login handler with a stub PVE client that
//     requires OTP") for exactly this gap.
//   - GET /access/domains (realm listing): not part of docs/api.md's
//     documented routes at all (only /auth/login, /auth/logout, /auth/me
//     are), so "realm list from PVE" in T-105's task card is read here as
//     "forward whatever realm the caller supplies to PVE" (any PAM/PVE/
//     LDAP/AD/OIDC realm, docs/security.md), not a new vnprox endpoint.
//
// See this task's completion report for the full reasoning and what T-106
// (and later capability-gated route registrations) should assume.
package auth
