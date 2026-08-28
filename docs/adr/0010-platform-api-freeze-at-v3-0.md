# ADR-0010: Platform API freeze at v3.0

**D-number:** D10 (`docs/architecture.md` §10, fully enumerated in §13)
**Status:** Accepted (v3.0); extended additively since (T-2705 added four typed MCP staging
tools under this ADR's own additive-only rule — no existing tool was renamed, removed, or changed)

## Context

Three programmable surfaces opened up during Phases 11–17: the MCP tool manifest for AI
automation clients, the plugin SDK's extension-point interfaces, and the WebSocket `"events"`
stream schema. v3.0 was designated the platform release — the point at which third parties (plugin
authors, MCP/AI clients, anything consuming the event stream) need something they can build against
without fear of routine breakage. A platform with surfaces that change under integrators is not
usable as a platform.

## Decision

Freeze three surfaces as stable, documented compatibility contracts, enumerated exhaustively in
`docs/architecture.md` §13 (nothing outside that enumeration is a frozen contract):

- **The MCP tool manifest (§13.1)** — a fixed, enumerable thirteen-tool allowlist. No
  apply/confirm/approve/delete/rollback/discard tool exists or can exist; a package-load check
  rejects any tool name matching those verbs, enforced at compile time alongside an
  interface-surface test that the change-engine seam handed to the MCP server has no mutation
  method.
- **The plugin SDK interfaces (§13.2)** — the five extension points at `plugin.APIVersion == "v1"`.
- **The WebSocket events envelope (§13.3)** — the flat `{"event": "<name>", ...payload}` shape and
  the frozen event set.

The shared deprecation policy (the same one the changeset API adopted at v1.7): **additive-only**
within a version — new optional fields, new tools/events/extension points may be added freely — but
no field, tool, or event is ever renamed or removed, and no method signature changes in place. A
breaking change mints a new version, announced in both `docs/architecture.md` and `docs/api.md`,
with the previous version kept accepted for at least one minor release before removal.

## Consequences

**What this enables.** Third parties can build against vnprox with a real stability guarantee
instead of tracking `main`. This is the precondition for the ecosystem work built on top of it —
the Terraform provider, Ansible collection, and hosted plugin/blueprint registry all depend on a
contract that doesn't move under them. It is also enforced structurally in several places, not just
documented: the MCP manifest's verb-blocklist and interface checks run at compile/package-load
time, and the `internal/apicontract` golden-fixture suite (part of `make check`) is this repo's
enforcement of the changeset-API half of the same additive-only promise.

**What this costs / forecloses.** A frozen surface cannot be un-frozen casually. Fixing a design
mistake discovered in the v1 MCP manifest, the v1 plugin interfaces, or the WS envelope now
requires minting a v2 and running both versions in parallel for at least one minor release — real,
ongoing maintenance cost for every future correction, not a one-time tax. This already produced one
genuine near-miss worth recording as evidence rather than a hypothetical: T-2002 initially removed
`RulesetRef` from the frozen `simulate.path` MCP payload on the reasoning that grepping found "zero
in-repo consumers" — but an in-repo grep cannot see an external MCP client, so "no consumer found"
was necessary but not sufficient evidence for a frozen external contract. It was caught in review
before merge and reworked to keep the field (`planning/reports/T-504.md`), and two regression tests
(`internal/sim.TestRuleRef_JSONSchema_Stable`,
`cmd/vnproxd.TestMCPSimulatePath_FrozenPayloadFields`) now guard that specific shape — but the
near-miss demonstrates exactly the failure mode a freeze is supposed to prevent, and it slipped
past a repo-wide grep once already. That, in turn, makes the frozen-payload tests themselves part
of the compatibility contract's actual integrity: weakening or deleting one silently reopens the
freeze it was guarding, a risk this freeze design accepts in exchange for not needing a
schema-registry service to enforce it externally.

## See also

- `docs/architecture.md` §13 (the authoritative enumeration), §13.1–§13.3.
- `docs/api.md` (MCP server, WebSocket `/api/ws` sections).
- `internal/mcp/`, `internal/plugin/doc.go`, `internal/topology/hub.go`.
- `internal/*/frozen_mcp_payload_test.go` (the per-package frozen-shape regression tests).
- `planning/reports/T-504.md` (the near-miss).
