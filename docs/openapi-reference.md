# OpenAPI reference (T-3809)

vnproxd's HTTP API is generated into an OpenAPI 3.1 document from the router's own registered
routes (T-2405 — see [`api.md`'s "Machine-readable contract"](api.md#machine-readable-contract)
for how, and for the gate — `TestOpenAPI_EveryRouteIsDescribed` — that keeps it from drifting).
This page is that document, published: an interactive way to browse it, and a plain link to the
file itself.

- **[Open the interactive reference ↗](openapi-viewer.html)** — every path, method, path
  parameter, security scheme and error response, rendered from the live document with
  [Redoc](https://github.com/Redocly/redoc). Opens outside this docsify site (Redoc wants the
  whole page).
- **[Raw `openapi.json`](openapi.json)** — the same document, unrendered. What a code generator
  reads.
- The document is also served live by any running vnproxd, unauthenticated, at
  `GET /api/v1/openapi.json` — see `api.md`.

## What this document does and does not cover

Unchanged from `api.md`'s own note, restated here because this is the page a reader lands on
first: **every path, method, path parameter, security scheme and error response is complete and
mechanically checked.** Request and **response body schemas are not in it** — those live in the
route-by-route tables in [`api.md`](api.md), and describing them here is separate, not-yet-done
work. A client generated from this document alone gets correct routes, parameters and auth; it
does not get typed bodies. Routes belonging to a subsystem `testdata/dev.toml` leaves disabled
(the MCP transport, the plugin hub) are outside the completeness gate until that configuration
enables them — see `internal/apidoc/routes.go`'s own "KNOWN LIMIT" comment.

**A second, independent check exists on top of the gate above.** `web/tools/openapi-drift/check.mjs`
generates a TypeScript client from this document and cross-checks it against how
`web/src/api/*.ts` actually calls routes — it is wired as the `openapi-client` job in
`scripts/ci-local.sh` (GitHub Actions is unfunded for this repository; that local runner is the
gate that actually runs). It catches a route whose path shape changed in this document without
every frontend caller following; it does **not** catch response-body drift, for the same reason
the document itself doesn't carry body schemas. See that script's own header comment for the
precise list of what it catches and what it misses.

## Where this page is actually reachable

`docs.vnprox.com` does **not** currently resolve — no DNS record has been created for it yet (an
owner decision, not a technical blocker; see `docs/docs-site.md`'s "Versioning" section for the
GitHub Pages configuration this depends on). Until that DNS record exists, this site — and this
page — is reachable only at GitHub Pages' own default URL for this repository:
`https://bgovanlu.github.io/vnprox/docs/` (root-sourced Pages, `docs/` is a subpath of the
checkout, same as every other page linked from `_sidebar.md`). Anyone citing `docs.vnprox.com` for
this page today is citing a URL that does not work; use the GitHub Pages URL instead until the DNS
gap closes.
