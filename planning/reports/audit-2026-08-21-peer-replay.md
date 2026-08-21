# Audit finding · A legitimate repeated peer GET is rejected as a replay

**kind:** defect (correctness, federation) · **found by:** full audit, 2026-08-21 · **severity:**
high — it drops real peer reads in production and hides behind a WARN

## The evidence

The deployed instance on `pvecube` logs **2,885 rejections in 24 hours** (~2/min):

```
{"level":"WARN","msg":"peer: rejected unauthorized request","method":"GET",
 "path":"/api/peer/host/neighbors","remote_addr":"192.168.1.7:2838",
 "reason":"replayed peer request"}
```

These are not attacks. `192.168.1.7` is a second vnprox instance
(`GET /api/v1/health` → `4.0.0+39+g0f970685+dirty`), federated with this one.

## The mechanism

`internal/peer/sign.go` signs exactly four things, and there is **no nonce**:

```go
func canonicalRequest(method, requestURI string, bodyHash [sha256.Size]byte, ts int64) []byte {
    // method \n requestURI \n hex(sha256(body)) \n ts
}
```

`ts` is unix **seconds** (`internal/peer/middleware.go:103`, `skew := now.Unix() - ts`).

`internal/peer/middleware.go:136` then rejects any signature seen before:

```go
if s.replay.seenBefore(sigHeader, now) {
    s.rejectUnauthorized(w, r, "replayed peer request")
}
```

So two `GET /api/peer/host/neighbors` requests issued in the **same wall-clock second** — same
method, same URI, empty body, same `ts` — produce a byte-identical canonical string, therefore an
identical HMAC, therefore the second one is rejected.

## Why the code believes this is safe

`internal/peer/middleware.go:21-26` states the reasoning, and it is where the error is:

> Because the signature already covers method+path+body+timestamp, two requests can only collide on
> it by being byte-identical (cryptographically infeasible otherwise), so keying purely on the
> signature string is sufficient.

The parenthetical is true and irrelevant. It rules out *two different requests* colliding. It says
nothing about *the same request legitimately recurring*, which is not cryptographically infeasible —
it is the normal behaviour of any poller with a sub-second duty cycle and a one-second timestamp.
The comment conflates "no forgery" with "no repetition".

## Impact

- Real peer reads are silently dropped. The caller sees 401; `internal/collect` keeps last-known
  state and logs a WARN. **Federation data goes stale without any finding being raised** — in a
  product whose entire purpose is surfacing exactly this class of problem.
- It scales the wrong way: the more responsive federation is, the more often two polls land in one
  second, so the defect gets *worse* as the deployment gets healthier.
- It is invisible to the test suite, because no test issues the same signed GET twice inside one
  second.

## Fix options, in preference order

1. **Add a nonce to the canonical string** (random 128-bit, sent as a header, cached instead of the
   signature). This is the standard construction and makes "identical request twice" expressible.
   The replay cache keys on the nonce, and the "byte-identical" reasoning becomes actually true.
2. **Exempt idempotent reads.** A replayed `GET` of a read-only peer endpoint has no side effect to
   replay; the window check (±30s) already bounds staleness. Cheapest correct change, but leaves the
   asymmetry as a thing to remember.
3. **Millisecond timestamps.** Narrows the collision window without closing it. Not sufficient
   alone — two polls can still share a millisecond — so this is a mitigation, not a fix.

Recommend **1**, with **2** as the immediate stop-gap if a release is imminent.

## What this does not establish

Only one federated pair was observed, and the peer is 41 commits behind and built `+dirty`, so the
*rate* here is not representative. The mechanism, though, is read off the signing code and does not
depend on the peer's version.
