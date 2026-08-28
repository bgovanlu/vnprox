// SPDX-License-Identifier: Apache-2.0

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
// # pvemock fidelity notes
//
// internal/pvemock implements the PVE surface this package's production
// code depends on, so the integration tests here run the genuine paths:
//
//   - GET /access/permissions (capability derivation's data source):
//     implemented by the mock, which reports the fixture user's flat
//     UserSpec.Privileges list at ACL path "/" (real PVE returns a
//     per-path tree and enumerates concrete privileges instead of a "*"
//     wildcard — the exact real-PVE response shape still needs hardware
//     validation). This package's integration tests use the unmodified
//     production identity factory (NewClientIdentityFactory), so login,
//     permission derivation, ticket renewal, and node enumeration all go
//     out over real HTTP to the mock.
//   - TOTP/second-factor: the mock's handleTicket checks a fixture user's
//     static "totp" code against the request's "otp" field (missing/wrong
//     code → 401), so the OTP passthrough (the "otp" field on POST
//     /auth/login forwarded to pve.Config.OTP and on to PVE's POST
//     /access/ticket at first login) is integration tested end-to-end
//     (TestIntegration_TOTPLoginAgainstMock) against single-node.yaml's
//     totp-user@pve. A handler-level unit test with a stub PVEIdentity
//     (TestHandleLogin_OTPPassthrough) additionally covers the handler's
//     own error mapping in isolation. Historical note: an earlier
//     revision of this comment claimed the T-105 task card "explicitly
//     sanctions" testing OTP via the stub alone; the card contains no
//     such wording — it asks for a "full login/logout/me cycle against
//     pvemock incl. a TOTP-required fixture user", which is what the
//     integration test now provides. Real PVE's two-step TFA
//     ticket-challenge flow is not modeled by the mock and needs hardware
//     validation.
//   - GET /access/domains (realm listing): not part of docs/api.md's
//     documented routes at all (only /auth/login, /auth/logout, /auth/me
//     are), so "realm list from PVE" in T-105's task card is read here as
//     "forward whatever realm the caller supplies to PVE" (any PAM/PVE/
//     LDAP/AD/OIDC realm, docs/security.md), not a new vnprox endpoint.
//     This remains a deviation to revisit if the UI ever needs a realm
//     dropdown.
package auth
