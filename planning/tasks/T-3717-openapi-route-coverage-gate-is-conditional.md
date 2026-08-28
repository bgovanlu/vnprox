# T-3717 · `TestOpenAPI_EveryRouteIsDescribed` is green because the routes it misses never mount

**Found by:** T-3809's drift-tripwire work, 2026-08-27 · **size:** S ·
**depends:** — · **affects:** T-2405 (OpenAPI generator), T-3809 (published spec), and any
consumer who trusts `docs/openapi.json` to be complete

## The observation

`internal/apidoc` generates `docs/openapi.json` from the router's **actual** registered routes, and
`TestOpenAPI_EveryRouteIsDescribed` asserts every route is documented. That test passes. The spec
carries 255 operations.

**It is nonetheless incomplete**, and the test cannot see it. `internal/apidoc/routes.go`'s own
header says why:

> "…daemon brought up under `testdata/dev.toml`. A route whose service is nil in
> [that config is not mounted]"

So the enumeration is of *routes mounted under one test config*, not of routes the product serves.
Any route whose backing service is nil in `testdata/dev.toml` is invisible to both the generator
and the gate — it is not merely undocumented, it is undocumented *and* unnoticed.

Verified absent from `docs/openapi.json` while being real, registered routes:

| Route | Registered at |
|---|---|
| `GET /firewall/analytics` | `internal/api/fwlog.go:39` |
| `GET /hub/index` | `internal/api/hub.go:178` |
| `POST /hub/install` | `internal/api/hub.go` (documented in the package header) |

(`GET /firewall/log`, named alongside these in T-3809's report, **is** present in the spec — the
report slightly over-counted. Re-derived here rather than inherited.)

Separately and legitimately, `/inventory/*` and `/ipam/subnets/*` are chi wildcards that
`internal/apidoc/doc.go` deliberately excludes because OpenAPI has no path-template expression for
"everything below this prefix." That exclusion is a documented design limit and is **not** part of
this defect.

## Why this matters more now than it did last week

The spec is now **published** (T-3809) at the GitHub Pages URL as the API reference, and a
generated-client drift tripwire checks frontend call sites against it. Three real routes missing
from the reference means: a consumer reading it concludes those endpoints do not exist, and the
new tripwire has to carry them as allowlisted exceptions — which is how a gap becomes permanent
furniture.

This is the session's recurring shape once more: **a gate that is green because the case that
would fail it is never exercised.** It is the same mechanism as `internal/pvemock` fixtures
agreeing with the docs they were written from, and as the tenant privilege gap that read as open
for eight days after being fixed.

## Deliverables

- Make route enumeration independent of which services happen to be non-nil in one config.
  Options, in rough order of preference: enumerate against a config where **every** service is
  present (a doc-generation config whose only job is completeness); or enumerate the route table
  structurally, before service wiring; or, at minimum, assert that the set of nil services in
  `testdata/dev.toml` is **empty**, so the config itself can no longer silently hide routes.
- Document the three missing routes in the spec, with the same fidelity as their neighbours.
- A test that fails when a route is registered but unreachable by the generator — i.e. the gate
  the current one only appears to be. Prove it fails by nil-ing a service and watching it catch
  the route.
- Once fixed, remove the corresponding entries from `web/tools/openapi-drift/check.mjs`'s
  allowlist and confirm the job still passes; the allowlist should shrink to the two legitimate
  wildcard exclusions.

## Acceptance criteria

1. `docs/openapi.json` contains `/firewall/analytics`, `/hub/index` and `/hub/install`.
2. Setting any service to nil in the doc-generation config makes the coverage test **fail**,
   demonstrated, rather than silently shrinking the spec.
3. `web/tools/openapi-drift/check.mjs`'s allowlist contains only the two documented chi-wildcard
   prefixes, and `scripts/ci-local.sh openapi-client` passes.
