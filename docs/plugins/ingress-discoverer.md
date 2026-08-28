# `ingressDiscoverer`

See [plugin-development.md](../plugin-development.md) for the SDK overview,
the stage-only boundary, and the security section this page does not repeat.
This point reuses `internal/ingress.IngressDiscoverer` **verbatim** — the SDK
does not fork a parallel contract for it.

## Interface (`internal/ingress/model.go`)

```go
// Target is one operator-configured reverse-proxy endpoint to poll — the
// in-memory shape an ingress_targets row is decoded into before a Discover
// call.
type Target struct {
	ID string
	// Kind selects which registered IngressDiscoverer handles this target.
	Kind Kind
	// Address is the target's own status/admin endpoint base URL, e.g.
	// "http://10.0.0.5:8404" for an HAProxy stats page, or
	// "http://10.0.0.7:2019" for a Caddy admin API. Each discoverer
	// documents exactly which path it appends.
	Address string
	// Credential is an optional decrypted bearer-token/basic-auth secret,
	// held in memory only for the duration of one Discover call — never
	// logged, never returned by any API response.
	Credential string
}

type ProxyState struct {
	TargetID string
	Kind     Kind
	// Error is set when Reachable is false — a human-readable reason
	// (connection refused, non-2xx status, malformed body, ...).
	Error     string
	Backends  []Backend
	Reachable bool
}

// IngressDiscoverer is the read-only reverse-proxy discovery seam T-1702's
// future plugin SDK will make pluggable: exactly one method, taking a
// Target and returning its currently discovered ProxyState. Every
// implementation in this package issues only read-only HTTP GET requests
// against Target.Address — never a mutating call — and never any target this
// interface wasn't handed.
type IngressDiscoverer interface {
	Discover(ctx context.Context, target Target) (ProxyState, error)
}
```

(`Backend` — route/address/detail/healthy per backend the target reports.)

A plugin registers a new proxy vendor by attaching an `IngressDiscoverer` for
a new `Kind` your manifest names via `Registration.IngressKind` — the
registry merges it into the built-in `ingress.Registry` by `Kind`, and **a
plugin `Kind` never overrides a built-in `Kind`**
(`plugin.Registry.IngressRegistry`): a built-in vendor is always
authoritative over a plugin claiming the same name.

Minimum capability to attach this point: `netRead`.

## What the host guarantees

- **You are handed exactly the operator-configured `Target`** — its
  `Address` and, when configured, a freshly decrypted `Credential` valid for
  this one call only. You never see the sealed/encrypted form and never see
  a target the operator didn't configure for your `Kind`.
- **Your `Kind` cannot shadow a built-in vendor.** If you register
  `Kind("haproxy")`, the built-in HAProxy discoverer still wins — this
  protects an operator from a plugin silently intercepting traffic meant for
  a shipped discoverer.
- **A discovery failure is a normal, well-formed result, not necessarily an
  error return.** `ProxyState{Reachable: false, Error: "..."}` is the
  expected shape for "the target didn't answer" — reserve a Go `error`
  return for something the caller should treat as your own decode failure.

## What the plugin must not do

- **Every request must be read-only.** `internal/ingress`'s own doc
  comment states the invariant this interface exists to preserve: issue only
  GET-class requests against `Target.Address`; never a mutating call to the
  proxy you're discovering.
- **Never log `Credential`**, or return it anywhere in `ProxyState` — it is
  handed to you decrypted, for the duration of one call, specifically so it
  never needs to leave your function's stack.
- **Never call anything other than `Target.Address`.** The `Target` you're
  handed is the entire scope of what you may reach — no DNS-guessing a
  related host, no following a redirect to a different origin without
  reconsidering whether that's still "the target."
- **Must not block indefinitely** — respect `ctx.Done()`; a hung discoverer
  degrades every other target's status refresh cycle if it shares a
  scheduling budget with them.

## Minimal working example

From `internal/plugin/plugintest/samples.go` — the SDK's own fixture:

```go
type sampleIngressDiscoverer struct{}

func (sampleIngressDiscoverer) Discover(_ context.Context, target ingress.Target) (ingress.ProxyState, error) {
	return ingress.ProxyState{
		TargetID:  target.ID,
		Kind:      target.Kind,
		Reachable: true,
		Backends: []ingress.Backend{{
			Route:   "sample-route",
			Address: "10.0.0.9:8080",
			Healthy: true,
		}},
	}, nil
}
```

Wire it into a `plugin.Manifest` declaring `plugin.ExtIngressDiscoverer` and
`netRead`, and a `plugin.Registration` with both `IngressDiscoverer` and
`IngressKind` set (`checkImpl` refuses one without the other) — the same
shape [`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template)
uses for `findingProducer`; swap the extension point and the implementation
fields.
