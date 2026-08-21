# Audit finding · Peer responses are cancelled before their body is read

**kind:** defect (correctness, federation) · **found by:** full audit, 2026-08-21 · **severity:**
high — it is the single largest source of log volume in production and it breaks peer host polling

## The evidence

The deployed instance on `pvecube` logs **17,197 occurrences in 24 hours** (~12/min), the largest
warning class by a wide margin:

```
{"level":"WARN","msg":"collect: peer host poll failed, keeping last-known state",
 "node":"pve001","peer_addr":"192.168.1.7:8007",
 "error":"host links (pve001): context canceled"}
```

`context canceled` — not `DeadlineExceeded`. Something called `cancel()`; a timeout did not expire.

## The mechanism

`internal/peer/client.go:223` wraps each request in its own cancellable context:

```go
reqCtx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
defer cancel()
...
resp, err := c.httpClient.Do(req)
...
return resp, nil          // <-- body NOT read yet; defer cancel() fires HERE
```

The caller then reads the body, in a different function:

```go
func decodeInto(resp *http.Response, out any) error {
    defer func() { _ = resp.Body.Close() }()
    ...
    return json.NewDecoder(resp.Body).Decode(out)   // client.go:295
}
```

An `*http.Response`'s `Body` stays bound to the request's context. `defer cancel()` runs when `do()`
returns, so by the time `decodeInto` reads, the context is already cancelled and the read fails with
`context canceled`.

**Why it is intermittent rather than total.** It is a race against the transport's buffer. A
response small enough to have been fully buffered before `cancel()` decodes fine from memory; a
larger one still streaming fails. That is exactly the observed pattern — `host links` (one entry per
interface, the biggest peer payload) fails constantly, while smaller peer calls succeed, which is
why federation appears to half-work rather than to be broken.

## The fix is a deletion

`internal/peer/client.go:139` already sets the same bound on the client itself:

```go
hc = &http.Client{Timeout: opts.RequestTimeout, Transport: trust}
```

`http.Client.Timeout` covers connect, redirects **and body reads** — it is the correct construct for
exactly this, and it is already in place. The per-request `context.WithTimeout` + `defer cancel()`
adds nothing except the bug.

Recommended: **remove the `reqCtx`/`cancel` pair** and build the request with the caller's `ctx`
(`http.NewRequestWithContext(ctx, ...)`) so caller cancellation still propagates, and let
`http.Client.Timeout` bound the call.

The alternative — hand the cancel func to the caller so it fires after the body is consumed — works
but spreads lifetime management across two functions for no gain over the deletion.

## Impact

- **Peer host polling does not work for any node whose link payload exceeds the transport buffer.**
  `internal/collect/host.go:169` keeps last-known state and logs a WARN, so the graph silently
  serves stale peer data instead of reporting a failure the operator can act on.
- It is the largest log producer on the deployed instance — 17,197 lines/day of a WARN that is
  vnprox's own bug, which is also how it stayed invisible: it looks like an unhealthy peer.
- It scales with cluster size: the more interfaces a peer node has, the more reliably it fails.

## Why no test caught it

Every peer test runs against an in-process server on loopback with small fixtures, where responses
are buffered before `cancel()` lands — so the race resolves the benign way every time. A regression
test needs either a deliberately large body or a synchronisation point between `do()` returning and
the body being read.

## What this does not establish

One federated pair was observed, and the peer is 41 commits behind and built `+dirty`. The peer's
version is irrelevant to this mechanism — it is entirely local to the client — but the observed
*rate* is specific to this deployment.

## Reproduction (not inference)

The mechanism above is read off the code, so it was reproduced standalone before being filed —
`do()` and `decodeInto()` copied verbatim in shape, against an `httptest` server that keeps
streaming for 60ms after the first flush:

```
      1 link entries -> ok, decoded 1 entries
    100 link entries -> ok, decoded 100 entries
   5000 link entries -> decode error: context canceled
  50000 link entries -> decode error: context canceled
```

Both halves of the claim hold: the failure is real, and it is **size-dependent** exactly as
predicted — small responses decode from the transport's buffer, larger ones do not. The error string
is character-identical to the one in production.

That size threshold is why this reads as "a flaky peer" rather than "a bug in our client": a lab
node with three interfaces never trips it, and a real node with dozens does.
