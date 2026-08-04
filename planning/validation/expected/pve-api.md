# Expected outcomes — pve-api

Backs `planning/validation/harness/pve-api.sh`. See `planning/validation/README.md` for the table
format and how to run triage against a returned blob.

**Caveat that applies to every row below**: on a real PVE node the harness prefers `pvesh`
(present on every stock install) over the HTTP fallback pvemock testing uses. `pvesh
--output-format json`'s exact output shape (the full `{"data": ...}` envelope vs. just the
unwrapped payload) has not itself been confirmed against real PVE — this is a candidate first
finding for T-1802, not an assumption to trust blindly. `exit_code` is therefore the primary
mechanical check below; `raw`-content rows use substrings chosen to survive either shape, and a
human/triage pass should still eyeball `raw` for the checklist's real question (exact wire shape,
exact wording), not just the mechanical pass/fail.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| pve-api-01 | exit_code | equals | 0 | API-token auth failed outright (curl couldn't even complete the request) — check network/DNS/cert issues before concluding token auth itself is broken. If `raw` is `skipped: PVE_API_TOKEN not configured`, re-run with `PVE_API_TOKEN` set to a real token before treating this row as evidence at all. |
| pve-api-01 | raw | contains | http_status=200 | Real PVE rejected the `Authorization: PVEAPIToken=user@realm!id=secret` header — confirm the header shape matches `internal/pve`'s implementation exactly (no cookie/CSRF expected) and capture the real status code/body in the burndown notes. This is also where **token privilege separation (privsep)** needs a second look: pvemock models a token as carrying the owner's full privileges: confirm whether a real, deliberately restricted token still succeeds here or is scoped differently than its owning user. |
| pve-api-02 | exit_code | equals | 0 | Ticket-as-password renewal command failed to run — check the harness ran `curl` successfully at all. |
| pve-api-02 | raw | contains | renewal_http_status=200 | Real PVE does not accept a still-valid ticket as the `password` field on `POST /access/ticket` — this would mean `internal/pve/auth.go`'s renewal path is broken against real hardware and needs its own bug card. Also confirm this near ticket expiry and against a TFA-enabled realm per the checklist item's full text. |
| pve-api-03 | exit_code | equals | 0 | `GET /access/permissions` failed outright. |
| pve-api-03 | raw | contains | data | The response didn't come back as a `{"data": ...}` envelope at all — capture the full `raw` value as the real per-path ACL tree shape (concrete privilege names, not a flat `"*"` list) and file a bug card against `internal/auth/caps.go`'s `BuildCapabilities` if it can't parse the real shape. |
| pve-api-04 | raw | contains | without_otp_http_status=401 | A TOTP-required user's login succeeded **without** an OTP — that would be a real authentication-bypass finding, escalate immediately rather than filing an ordinary divergence. |
| pve-api-04 | raw | contains | with_otp_http_status=200 | A TOTP-required user's login failed **with** the correct OTP — either the fixture credentials (`PVE_TOTP_USER`/`PASSWORD`/`CODE`) don't match a real TFA-enabled user on this node, or real PVE's modern two-step NeedTFA challenge flow rejects the single-step `otp=` passthrough `internal/pve` implements (the checklist's stated open question). |
