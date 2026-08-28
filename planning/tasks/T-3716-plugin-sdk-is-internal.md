# T-3716 · The frozen plugin SDK lives under `internal/`, so no third party can import it

**Found by:** T-3811's plugin developer portal work, 2026-08-27 · **size:** M ·
**depends:** — · **affects:** D10 (platform API freeze), T-1702 (plugin SDK), the whole
"open platform" arc, and every hub-registry plugin feature

## The observation

The plugin SDK's five extension points are frozen at `APIVersion == "v1"` under D10 —
`docs/architecture.md` calls them a "stable, documented compatibility contract," and the hub
registry exists to distribute third-party plugins that implement them.

**They live in `github.com/bgovanlu/vnprox/internal/plugin`.**

Go's internal-package rule makes a package under `internal/` importable only from within the
subtree rooted at that `internal/`'s parent — here, `github.com/bgovanlu/vnprox` itself. This is a
compiler-enforced language rule, not a convention and not something a build tag or a `replace`
directive can waive. Consequences, stated plainly:

- **An in-process plugin can only be built from inside a vnprox checkout.** There is no way to
  publish one as an independent module or repository.
- **Even the out-of-process Go helper is unreachable**: `internal/plugin/procshim` is under the
  same barrier, so a third-party Go author cannot use `procshim.Serve` either.
- A genuinely external author's only path is to hand-implement `procshim`'s length-delimited JSON
  framing against `wire.proto` directly — doable in any language, but it is a wire protocol, not
  an SDK.

None of this makes the plugin system non-functional; the out-of-process path works and is
language-agnostic, which is a real strength. **What it makes false is the framing.** A "plugin
SDK" that the intended audience structurally cannot import is a wire protocol with documentation,
and the difference matters most to exactly the people open-sourcing is meant to attract.

## Why this was invisible until now

Every plugin ever written for vnprox was written *inside* this repository — the SDK's own
fixtures (`internal/plugin/plugintest/samples.go`), the conformance harness, and now
`examples/plugin-template/`. All of them compile precisely because they are inside the module. The
constraint only appears the moment someone builds a plugin from outside, which no one has done.
This is the same shape as the rest of this session's findings: a claim that stayed true-looking
because nobody exercised the case that would refute it.

## Deliverables

Pick one, deliberately, and record why:

1. **Promote the SDK to a public package** — move the frozen v1 interfaces (and `procshim`) to
   `pkg/plugin` or a sibling module. This is the option that makes the platform story true. Note
   it interacts with D10: the interfaces are frozen, so the *move* must preserve them exactly, and
   the import path change is itself a breaking change for in-repo callers even though the types
   are identical.
2. **Publish a separate SDK module** (`github.com/bgovanlu/vnprox-plugin-sdk`) mirroring the
   frozen interfaces, with a test in this repo asserting the two definitions stay identical — the
   same anti-drift shape used elsewhere in the codebase. Costs a second release surface.
3. **Accept it and correct the framing everywhere** — stop calling it an SDK for third parties,
   document the wire protocol as *the* third-party contract, and make the out-of-process path
   first-class with a published `wire.proto` and at least one non-Go reference implementation.

Option 3 is legitimate and is the cheapest honest answer. What is not acceptable is leaving
documentation that implies option 1 while the code enforces option 3.

## Acceptance criteria

1. A plugin built **outside** this module either compiles against a published SDK package (options
   1/2) or is demonstrably implementable from the documented wire protocol alone (option 3), with
   a worked example that was actually built and run from outside the repo.
2. `docs/plugin-development.md`, `docs/architecture.md` §11, and the D10 ADR agree with whichever
   reality is chosen — no page implies an import path that does not work.
3. If option 1 or 2: a test proves the frozen v1 interfaces are byte-identical across the move, so
   D10's freeze survives it.
