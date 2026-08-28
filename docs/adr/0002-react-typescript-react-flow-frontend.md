# ADR-0002: React + TypeScript + React Flow frontend, embedded

**D-number:** D2 (`docs/architecture.md` §10)
**Status:** Accepted

> `docs/roadmap-proven.md` also has its own unrelated "D2" (that arc's hardware scope: `pvecube`
> only). See `docs/adr/README.md`'s numbering-collision table.

## Context

vnprox's core product surface is a visual, interactive network topology canvas — the thing that
makes SDN, firewall rules, and physical/virtual/overlay layering *visible* instead of a stack of
opaque PVE config files (see `docs/features/sdn.md`'s framing: "Proxmox SDN... is powerful but
opaque in the stock UI. vnprox makes it visual, guided, and observable"). That needs a real
interactive graph-editing canvas with live updates, not a set of CRUD forms.

## Decision

React 18 + TypeScript (strict) + Vite, in `web/`, embedded into `vnproxd` via Go's `embed.FS` so
ADR-0001's single-binary deploy still holds even though the UI has its own build step. The canvas
is `@xyflow/react` (React Flow) with a custom layered layout (`elkjs`), custom node types per
entity kind, per-layer visibility toggles (physical / L2 / SDN-overlay / guests), and edge bundling
for VLAN trunks. Server state is TanStack Query with a WebSocket bridge applying `*.delta` events
into query caches; client-only UI state is zustand. Styling is Tailwind CSS + Radix primitives,
dark-mode default (matching PVE admin ergonomics). Every user-facing mutation flows through one
`ChangesetDrawer` UX — edit → review diff → apply with confirm countdown — never a fire-and-forget
dialog, which is this decision's UI-level enforcement of ADR-0004.

## Consequences

**What this enables.** A mature, widely-used graph-editing ecosystem (React Flow + elkjs) instead
of building interactive canvas rendering, drag/connect interactions, and layout from scratch —
a materially different level of product investment than vnprox could otherwise afford. Strict
TypeScript with `CLAUDE.md`'s "no `any`, no unchecked casts" rule keeps a UI surface that has
roughly doubled in scope (per `docs/roadmap-proven.md` Phase 20's accessibility note) typed against
a growing, versioned API (`docs/openapi.json`) rather than drifting silently. Embedding via
`embed.FS` means ADR-0001's "one binary, no runtime deps" promise survives having a real SPA at
all — there is no separate static-file server to stand up or misconfigure.

**What this costs / forecloses.** React Flow and elkjs are now load-bearing third-party
dependencies for the single most visible part of the product; they sit outside ADR-0010's platform
API freeze entirely (that freeze covers MCP/plugin SDK/WS events, not frontend libraries), so an
upstream breaking change in either is a real, unmanaged risk with no contractual protection. The
embedded-SPA model requires a Node/npm/Vite toolchain in the release pipeline even though the
*runtime* has zero such dependency — `docs/development.md`'s tech stack section and `scripts/ci-local.sh`
both have to pin and check a second toolchain (Node 22 via nvm) alongside Go's. Strict TS and the
no-`any`/no-unchecked-casts rule are a standing tax on every future frontend change — real
discipline, paid on every PR, not a one-time cost. And the dark-mode-default, Tailwind/Radix
design system is now the baseline every new screen has to match, which Phase 20's own
"accessibility and design-system second pass" item exists to keep honest as the surface grows.

## See also

- `docs/architecture.md` §8 (frontend architecture).
- `docs/development.md` (frontend tech stack, component conventions).
- `web/src/` (the implementation).
