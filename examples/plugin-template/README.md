# plugintemplate

A complete, minimal, compiling vnprox plugin. It attaches to one extension
point — `findingProducer`, the simplest of the SDK's five — and does nothing
else: it returns one static example finding.

This is also the literal source `vnproxctl plugin scaffold <name>` stamps
out (see `doc.go`'s embed). Build and test it exactly as it sits here to see
what a freshly scaffolded plugin looks like before you rename anything.

## What's here

| File | What |
|---|---|
| `manifest.go` | `plugin.Manifest` (identity, SDK version, transport, extension points, capability scope) and `plugin.Registration` (manifest + implementation) |
| `producer.go` | The `plugin.FindingProducer` implementation — one method, `Produce` |
| `producer_test.go` | A direct test of `Produce`, a demonstration of `internal/plugin/plugintest`'s conformance harness, and a test that installs this plugin through the real `plugin.Registry` |
| `README.md` | This file |

## Build and test

From the repository root:

```sh
go build ./examples/plugin-template/...
go test ./examples/plugin-template/...
```

## Why this can't be its own repository (yet)

`manifest.go` and `producer.go` import `internal/plugin`; `producer_test.go`
also imports `internal/plugin/plugintest`. Both are Go **internal** packages
— the compiler itself refuses to let code outside
`github.com/bgovanlu/vnprox` import them, no matter what import path a
separate repository writes. So an in-process plugin (this template's
transport) has to be built from inside a checkout of this repository today.

If you want a plugin that ships as its own independent project, use the
**out-of-process** transport instead: it never imports vnprox's Go code at
all. It speaks `internal/plugin/procshim`'s documented wire protocol
(`internal/plugin/procshim/wire.proto` — length-delimited JSON over your
process's own stdin/stdout, a fixed and enumerable method vocabulary) from
whatever language and build system you like. See
[`docs/plugin-development.md`](../../docs/plugin-development.md)'s
"In-process vs. out-of-process" section for the tradeoffs.

## Extend it

1. Rename the package, `ManifestID`, and the display `Name` in `manifest.go`
   to your own plugin's identity (or run `vnproxctl plugin scaffold
   <your-name>` again from a clean checkout, which does this for you).
2. Replace `producer.go`'s `produceFindings` with your own read-only
   detection logic.
3. If you need to stage a remediation for a human to review, have your
   `Producer` implement `plugin.HostConsumer` and hold the
   capability-scoped, stage-only `plugin.Stager` it is handed at install —
   never anything broader. See
   [`docs/plugins/finding-producer.md`](../../docs/plugins/finding-producer.md).
4. Add extension points by implementing more of `plugin.SwitchDriver`,
   `plugin.FlowIngestor`, `plugin.IngressDiscoverer`, or
   `plugin.DashboardTileProvider`, wiring each into `Registration` and listing
   it in `Manifest.ExtensionPoints`, and widening `Capabilities` to cover
   each new point's minimum capability (`internal/plugin/caps.go`'s
   `extensionPointMinCap`).

## The safety boundary this template inherits

A `FindingProducer` is read-only by construction: `Produce` takes a context
and returns findings, nothing else reachable from the interface. Nothing
here can apply a network change, and nothing you add to this template can
either, without also implementing a different extension point — and even
then, the only change-engine surface any extension point can reach is
`plugin.Stager`'s `Create`/`Validate` (stage-only; no `Apply`/`Confirm`/
`Rollback` is reachable from plugin code, in-process or out-of-process). See
[`docs/plugin-development.md`](../../docs/plugin-development.md) for the
full account.
