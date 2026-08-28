# Plugin development

vnprox ships a small, versioned extension SDK (`internal/plugin`, T-1702):
five extension points a plugin can attach to, a capability-scoped registry
that installs/enables/disables/uninstalls them, and one structural rule that
never bends — **a plugin can stage a change; it can never apply one.**

This page is the reader-facing walkthrough. It does not restate the SDK's
interface signatures — those are frozen at `plugin.APIVersion == "v1"`
(decision [D10](adr/0010-platform-api-freeze-at-v3-0.md)), and a doc that
copies a frozen interface risks drifting from the code that actually defines
it. The one-page-per-extension-point reference below embeds each interface
straight from source instead of paraphrasing it. If you read nothing else,
read [`internal/plugin/doc.go`](https://github.com/bgovanlu/vnprox/blob/main/internal/plugin/doc.go)
— it is this SDK's own package doc comment and the closest thing to ground
truth this document can point at.

## The five extension points

| Extension point | v1 interface | Class | Min capability | Reference |
|---|---|---|---|---|
| `switchDriver` | `internal/switchdrv.SwitchDriver` (reused verbatim) | write-adjacent, dark-by-default | `netWrite` | [switch-driver.md](plugins/switch-driver.md) |
| `flowIngestor` | `plugin.FlowIngestor` | read-only decode | `netRead` | [flow-ingestor.md](plugins/flow-ingestor.md) |
| `findingProducer` | `plugin.FindingProducer` | read-only | `netRead` | [finding-producer.md](plugins/finding-producer.md) |
| `ingressDiscoverer` | `internal/ingress.IngressDiscoverer` (reused verbatim) | read-only | `netRead` | [ingress-discoverer.md](plugins/ingress-discoverer.md) |
| `dashboardTile` | `plugin.DashboardTileProvider` | read-only | `netRead` | [dashboard-tile.md](plugins/dashboard-tile.md) |

This table is `docs/architecture.md` §11's table, reproduced here for a
reader who lands on this page first. §11 and §13.2 are the authoritative
enumeration if the two ever disagree.

Two of the five points are not SDK-specific inventions: `switchDriver` and
`ingressDiscoverer` reuse an already-shipped, already-tested interface
verbatim (`internal/switchdrv`, `internal/ingress`) rather than forking a
parallel contract. The SDK adds three new points on top —
`flowIngestor`, `findingProducer`, `dashboardTile` — plus the registry,
capability scope, and stage-only boundary that govern all five.

## Pick the simplest point to start

If you are writing your first plugin, start with **`findingProducer`**: one
method, `Produce(ctx) ([]findings.Finding, error)`, strictly read-only, no
extra domain types to learn. `vnproxctl plugin scaffold` (below) generates
exactly this.

## The stage-only boundary

This is the one guarantee every other design decision here defers to. Copied
verbatim from `internal/plugin/stager.go`:

```go
// Stager is the ONLY change-engine surface a plugin can reach. It exposes
// exactly the stage-only pair — Create (stage a draft changeset) and
// Validate (run the validator pipeline over it) — and deliberately NOTHING
// else. There is no Apply, Confirm, or Rollback method on this interface,
// in-process or over the out-of-process transport: a plugin extends
// read/ingest/render seams and can stage work for a human to apply, but is
// never itself a mutation path (T-1702 AC3, verified by an interface-surface
// test). A human (or the confirm machinery) remains the sole apply
// authority, exactly as for every other changeset since T-205.
type Stager interface {
	Create(ctx context.Context, title string, ops []change.Op) (change.Changeset, error)
	Validate(ctx context.Context, changesetID string) (change.Changeset, error)
}
```

Three things make this true structurally, not just by convention:

- **No plugin holds a `*change.Service`, a store handle, or a raw PVE
  client.** The only host seam a plugin can be handed is `plugin.Host`
  (`internal/plugin/host.go`), and `Host` exposes exactly one method —
  `Stager() Stager` — nothing broader.
- **`Stager` itself has no apply verb.** `internal/plugin.frozen_interfaces_test.go`
  and `internal/plugin/surface_test.go` assert this by reflection against the
  live interface, not by a reviewer remembering to check; a future edit that
  adds an `Apply` method to `Stager` fails that test, not just a code review.
- **The capability check runs before staging, inside the SDK.** Every op a
  plugin tries to construct is mapped (`plugin.RequiredCap`) to the
  capability that op class already requires everywhere else in vnprox
  (`fw.*` → `fwWrite`, `sdn.*` → `sdnWrite`, everything else defaults to the
  strongest `netWrite`). If the plugin's declared `Scope` does not cover it,
  `Create` returns `ErrCapabilityExceeded` and stages nothing — a
  fail-closed default for any future op type the mapping hasn't special-cased
  yet.

Only a `FindingProducer` implementing `plugin.HostConsumer` (i.e. one that
wants to stage a remediation) ever sees a `Stager` at all; the other four
extension points never receive one.

## Getting started: `vnproxctl plugin scaffold`

```sh
vnproxctl plugin scaffold --out ./examples/mywidget "my widget"
```

writes a complete, compiling `findingProducer` plugin into
`./examples/mywidget/`: a `plugin.Manifest` + `plugin.Registration`
(`manifest.go`), the `plugin.FindingProducer` implementation
(`producer.go`), a test that exercises `internal/plugin/plugintest`'s
conformance harness and installs the plugin through the real
`plugin.Registry` (`producer_test.go`), and a README. It is a byte-identical
copy of [`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template)
with two literal tokens substituted — see that directory's own README for
what it looks like before renaming, and `cmd/vnproxctl/plugincmd.go` for the
scaffold command itself.

```
$ vnproxctl plugin scaffold --out ./examples/mywidget "my widget"
Wrote plugin scaffold "mywidget" to ./examples/mywidget
  examples/mywidget/manifest.go
  examples/mywidget/producer.go
  examples/mywidget/producer_test.go
  examples/mywidget/README.md

Build and test it from inside this vnprox checkout:
  go build ./examples/mywidget/...
  go test ./examples/mywidget/...

It imports an internal vnprox package, so it can only build from inside this
repository — see examples/mywidget/README.md's "Why this can't be its own repository (yet)".
```

`-o json` is supported, like every other `vnproxctl` command.

## In-process vs. out-of-process, and what that means for where your code lives

`plugin.Manifest.Transport` is one of two values, and the choice has a real
consequence for where you can develop, well before it matters to an
installed vnproxd:

- **`TransportInProcess`** — your extension implementations are ordinary Go
  values, compiled directly into the `vnproxd` binary. This is what the
  scaffold above produces.
- **`TransportGRPC`** ("grpc" is the transport class name; the wire is not
  actually gRPC — see below) — your plugin is a **separate OS process**
  vnproxd spawns and supervises, speaking `internal/plugin/procshim`'s
  length-delimited JSON wire protocol over its own stdin/stdout. This is the
  only transport the Hub can install from a downloaded manifest — "an
  in-process plugin is build-time Go code and cannot be materialized from a
  downloaded manifest" (`docs/api.md`'s Hub section).

**The consequence for you:** `internal/plugin` and `internal/plugin/procshim`
are Go `internal` packages. The Go compiler enforces that only code rooted
under `github.com/bgovanlu/vnprox` (this module) may import them — a
separately cloned repository cannot `go get` and import either package, no
matter what import path it writes. So:

- An **in-process** plugin can only be built from inside a checkout of this
  repository today (a fork, or a directory under `examples/`, like the
  scaffold's output). There is no packaging story for shipping an in-process
  plugin as an independent artifact — it is compiled into the binary you
  ship, full stop.
- An **out-of-process** plugin has no such restriction, but *only if you
  don't try to use `internal/plugin/procshim` as a Go library either* — that
  import is just as internal-blocked as `internal/plugin` itself. The actual
  wire contract is documented, language-agnostic, and small:
  `internal/plugin/procshim/wire.proto` specifies proto3-shaped JSON messages
  (matching every extension method 1:1) carried as a 4-byte big-endian length
  prefix followed by one JSON object per frame —
  `{"method": "<name>", "params": <message>}` for a request,
  `{"result": <message>}` or `{"error": "<string>"}` for a response. A plugin
  binary written in Go without importing `internal/*`, or in any other
  language, can implement this framing directly against that spec and be a
  fully conformant out-of-process plugin. This is the realistic path for a
  plugin distributed as its own project.

This document previously would have said nothing about this distinction,
which is exactly the kind of gap that produces a template nobody can
actually use outside this repository. Say it plainly instead: **today,
building a plugin means either working inside a vnprox checkout (in-process),
or hand-implementing a small, fully-specified JSON framing protocol
(out-of-process).** Neither is a published Go SDK module a third party can
`go get`. If that changes, this paragraph is the one to update.

## Installing a plugin

- **In-process:** wired into `cmd/vnproxd`'s composition root at build time
  (see `cmd/vnproxd/server.go`'s existing `plugin.NewRegistry` call) — there
  is no runtime "install" step for this transport; it ships with the binary
  or it doesn't exist.
- **Out-of-process, direct:** an operator (not a plugin author) uses the same
  `plugin.Registry.Install` your test in `producer_test.go` exercises, wired
  up by whatever installs `procshim`-backed registrations in `cmd/vnproxd`.
- **Out-of-process, via the Hub:** `vnproxctl hub publish --type plugin`
  signs a `{manifest, signature}` artifact; a registry operator reviews and
  indexes it (`vnproxctl hub index`); an operator's vnproxd browses the
  catalog and calls `POST /hub/install`, which downloads the manifest,
  verifies its signature, confirms the catalog entry's advertised
  capabilities agree with the artifact's own manifest
  (`hub.CapabilityMismatch` — see the security section below), and only then
  calls `plugin.Registry.Install`. See [`docs/hub-registry.md`](hub-registry.md)
  for the full publish/index/install/revoke mechanics.

No install path — direct or via the Hub — bypasses `plugin.Registry.Install`'s
scope validation. There is exactly one place a plugin's capability scope is
checked and bound, and every path reaches it.

## Security boundary

Read this section before installing any plugin you did not write, and before
asking someone else to install yours. It is the honest account, not the
marketing one.

**What a capability scope actually is.** A plugin's `Manifest.Capabilities`
is drawn entirely from `internal/auth`'s existing `AllCaps` vocabulary — the
SDK introduces no new privilege of its own. It is enforced as a **ceiling**,
checked in two places:

1. At install (`plugin.ValidateScope`): every extension point the plugin
   declares must be covered by a capability in its scope
   (`switchDriver` needs `netWrite`; the other four need `netRead`). A
   plugin declaring `findingProducer` with no `netRead` in scope is refused
   before it is ever loaded.
2. At every staged op (`scopedStager.checkScope`): each op's `RequiredCap`
   must be in scope, or the entire batch is refused with
   `ErrCapabilityExceeded` — all-or-nothing, never a partial stage.

A scope is recorded on the `plugins` row and audited on every
install/enable/disable/uninstall (`plugin.AuditInstall` etc.), so `GET
/audit` always shows what a plugin could touch and who put it there.

**What sandboxing an out-of-process plugin actually does.** A
`TransportGRPC` plugin runs as a subprocess vnproxd spawns and supervises. It
is never handed a database connection, a file descriptor into vnprox's own
storage, or any host object beyond the length-delimited pipe. A crashed or
hung subprocess surfaces as an ordinary error from the host-side adapter,
handled by the same graceful-degradation path a misbehaving in-process
plugin's error return gets (a skipped tile, an omitted finding pack — never
a crashed daemon).

**What sandboxing does *not* do.** The subprocess is an ordinary OS process
with an ordinary process's OS-level network egress — nothing in this
boundary constrains what a `TransportGRPC` plugin's own executable can dial
out to, read from disk within its own filesystem view, or otherwise do with
the CPU/network access its OS user has. This is stated here, not hidden
behind the word "sandboxed": the isolation is *process* isolation (no shared
memory, no host object references, supervised lifecycle), not a seccomp
jail, a container, or a network-namespace restriction. If you need the
latter, put it there yourself at the OS level — vnprox does not provide it.

**What installing a plugin means you are trusting.** For any plugin,
in-process or out: that its declared capability scope is the true ceiling of
what it *tries* to do (the SDK enforces the ceiling; it cannot make a plugin
honest about what it *attempts* within that ceiling — a `netRead`-scoped
`FindingProducer` that silently phones home every finding it computes is
inside its capability ceiling and outside anything this boundary catches).
For an out-of-process plugin specifically: that its executable, wherever it
actually runs from, is not doing something malicious with its own OS-level
network access — the residual risk `internal/plugin/procshim/doc.go` and
`internal/plugin/doc.go` both state plainly rather than engineer away.

**What the Hub's "vetted" badge does and does not mean.** A vetted badge
requires two independent things: the signer is in the installing operator's
own `[hub] vetted_signers` allowlist, *and* `internal/hubreg.AutomatedVetChecks`
recorded, at publish time, that the artifact's capability manifest is
well-formed and that the catalog entry's advertised capabilities agree with
the artifact's own manifest. **It has never meant a reproducible-build
check**, and as of T-3806, the reason is narrower and permanent rather than a
temporary gap: vnprox's own release `.deb` is now byte-reproducible
(`scripts/verify-reproducible.sh`), but **the registry never receives a
submitted plugin's executable at all** — only a `{manifest, signature}`
artifact; the manifest's `endpoint` names where the real binary is delivered,
out of band, entirely outside anything the registry stores. There is
structurally nothing here for a rebuild-and-compare check to compare
against, for any plugin, vetted or not, ever. `docs/hub-registry.md`'s
"Automated vetting" section carries the full account, and every
`VetResult` states this residual explicitly rather than staying silent
about it.

**What is *not* a residual risk, because it is enforced structurally rather
than by a plugin's good behavior:** the stage-only boundary above, and the
capability ceiling. Those two are the actual safety guarantee this SDK
makes. Everything else in this section is what remains true even when they
hold.

See also: `docs/security.md`'s "Plugin capability scope" entry (the same
account, in the context of vnprox's full threat model) and
`docs/hub-registry.md` (registry mechanics, signing, revocation).

## Where to go next

- One page per extension point, each with the real interface copied from
  source and a minimal working example: [switch-driver.md](plugins/switch-driver.md),
  [flow-ingestor.md](plugins/flow-ingestor.md),
  [finding-producer.md](plugins/finding-producer.md),
  [ingress-discoverer.md](plugins/ingress-discoverer.md),
  [dashboard-tile.md](plugins/dashboard-tile.md).
- [`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template) —
  the complete, tested, buildable template `vnproxctl plugin scaffold` copies.
- `docs/architecture.md` §11 (extension points + deprecation policy) and §13.2
  (the frozen-v1 platform contract).
- [ADR-0010](adr/0010-platform-api-freeze-at-v3-0.md) — why the interfaces
  are frozen and what that costs.
- `docs/hub-registry.md` — publishing, signing, vetting, and revocation for a
  distributable out-of-process plugin.
- `docs/security.md` — the plugin capability-scope model in vnprox's full
  threat model.
