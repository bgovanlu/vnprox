# `findingProducer`

The simplest extension point, and the one [`vnproxctl plugin
scaffold`](../plugin-development.md#getting-started-vnproxctl-plugin-scaffold)
generates. See [plugin-development.md](../plugin-development.md) for the SDK
overview, the stage-only boundary, and the security section this page does
not repeat.

## Interface (`internal/plugin/interfaces.go`)

```go
// FindingProducer is the finding-pack extension point: a plugin contributes
// additional read-only findings (a "finding pack") alongside the built-in
// producers in internal/findings. It is strictly read-only — it returns findings
// computed from whatever read-models the host exposes to it; it can never apply a
// remediation itself. A finding may be marked Fixable, but the fix is staged
// through the ordinary change-engine flow by a human, never by the producer.
type FindingProducer interface {
	// Produce returns this pack's current findings. An error degrades this one
	// pack (its findings are omitted) without failing the aggregate findings
	// response, the same graceful-degradation contract a dead out-of-process
	// plugin gets (T-1702 AC5).
	Produce(ctx context.Context) ([]findings.Finding, error)
}
```

`findings.Finding` (`internal/findings/types.go`) is the value you return:
`ID`, `Source`, `Check`, `Severity`, `Detail`, `DocsLink`, `Nodes`, `Refs`,
`Fixable`, `AckableAt`.

Minimum capability to attach this point: `netRead`
(`internal/plugin/caps.go`'s `extensionPointMinCap`).

## What the host guarantees

- **Called on every findings refresh**, alongside every built-in producer
  and every other installed `findingProducer` plugin
  (`plugin.Registry.PluginFindings`).
- **An error degrades only your pack.** If `Produce` returns a non-nil
  error, that call's findings are omitted from the aggregate `GET /findings`
  response — the response still succeeds with everyone else's findings
  intact. Your plugin is logged (`plugin finding producer degraded`), not
  crashed.
- **Dispatch order is stable and sorted by plugin id**, so a table-driven
  test against your plugin's contribution is deterministic.
- **`Produce` is called with the request's context** — respect
  cancellation/deadline like any other read path; there is no separate
  timeout budget carved out for plugins.

## What the plugin must not do

- **Cannot mutate anything directly.** There is no store handle, no PVE
  client, no `change.Service` reachable from this interface at all — not
  "discouraged," genuinely absent from the method signature.
- **Cannot assume its findings are the only ones an operator sees**, or that
  its `ID`s are globally unique against every other producer's namespace —
  pick an ID scheme that won't collide (the shared conformance fixture uses
  `"plugin.<name>.<check>"`; the SDK's own sample uses
  `"plugin.sample.finding"`).
- **Should not mark a finding `Fixable: true` unless it means it.** `Fixable`
  is a promise to the UI that a remediation can be staged for this finding
  through the ordinary change-engine flow — it is never a request for the
  plugin itself to apply anything, because it has no way to.
- **Must not block indefinitely.** A `FindingProducer` blocking past its
  context's deadline degrades the aggregate `GET /findings` response's
  latency for everyone, not just its own pack — respect `ctx.Done()`.

## Minimal working example

The full, buildable, tested version of this is
[`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template)
(`producer.go` + `manifest.go`), the exact source `vnproxctl plugin scaffold`
copies. Reproduced here at a glance:

```go
type Producer struct{}

func NewProducer() *Producer { return &Producer{} }

func (p *Producer) Produce(_ context.Context) ([]findings.Finding, error) {
	return []findings.Finding{
		{
			ID:       "plugin.plugintemplate.example",
			Source:   findings.Source("plugin"),
			Check:    "plugintemplate-example-check",
			Severity: "info",
			Detail:   "example finding produced by the plugintemplate scaffold",
			Fixable:  false,
		},
	}, nil
}
```

wired into a `plugin.Manifest` declaring exactly `plugin.ExtFindingProducer`
and exactly `netRead`, and a `plugin.Registration` pairing the two — see the
template's `manifest.go` for the complete, real code.

## If you need to stage a remediation

Implement `plugin.HostConsumer`'s `UseHost(h plugin.Host)` on your producer;
the registry calls it once, at install, before any `Produce` call. `h.Stager()`
gives you the capability-scoped, stage-only surface described in
[plugin-development.md](../plugin-development.md#the-stage-only-boundary) —
`Create`/`Validate` only. Nothing about `findingProducer` changes this: you
can stage a draft changeset for a human to review and apply, never apply one
yourself.
