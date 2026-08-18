# Demo mode (T-2801)

`vnproxd --demo` runs the whole product against a synthetic Proxmox VE
cluster built into the binary, with no PVE endpoint and no outbound network.

It exists because evaluating vnprox otherwise requires a Proxmox cluster,
which is a hard gate in front of every potential user — including the people
who would validate it on hardware the project does not have.

```
vnproxd --demo                          # zero configuration
vnproxd --demo --demo-dir ~/vnprox-demo # somewhere else
vnproxd --demo --config <file>          # a harness that must pick the port
```

The first form writes a config, a store and a throwaway self-signed TLS
keypair into `$XDG_STATE_HOME/vnprox-demo` (or `~/.local/state/vnprox-demo`)
on first run and reuses them afterwards. It needs no root: everything lives
under the invoking user's own state directory, and nothing is installed.

Log in with `root` / `vnprox-mock` / realm `pam` — the demo fixture's own
built-in superuser. There is no separate credential store; a demo login is a
real PVE ticket login against the embedded cluster.

## What makes it structural rather than a promise

Three separate mechanisms, in three packages, each of which would have to be
removed for a demo daemon to touch anything real.

**The transport cannot dial.** Every PVE client a demo daemon builds is
constructed with `internal/demo`'s `HTTPClient()`, whose `RoundTripper`
dispatches straight into an in-process `pvemock.Server`. It holds no
`net.Dialer`, opens no socket, and binds no port. `PVE.APIURL` is
`http://demo-cluster.invalid` — reserved by RFC 2606 and guaranteed never to
resolve — so a future code path that bypassed the transport would fail to
resolve rather than reach whatever is on `localhost:8006` (which, on a
Proxmox node, is pveproxy itself).

**The host reader is a fixture, not your machine.** Collectors read
`internal/host.FixtureReader` over the same embedded cluster fixture, so a
demo shows the synthetic cluster's interfaces, conntrack table and LLDP
neighbours — never the interfaces of the laptop it is being demonstrated on.

**Peer discovery is off.** A demo daemon builds no peer client and reports
zero peers, so every cluster fan-out in the binary — coordination, the
peer-API audit and snapshot readers, the API's cluster-merge handlers —
resolves to the local node. This one is not cosmetic: the demo fixture's
cluster status advertises `10.10.0.12` and `10.10.0.13`, which are ordinary
RFC1918 addresses that plenty of networks route somewhere. Every node's host
state comes from the cluster-wide fixture reader instead
(`collect.Config.HostServesCluster`).

## Writes

Every mutating API in demo mode returns a "would have" result and touches
nothing:

```
POST /api/v1/changesets
200 OK
{
  "demo": {
    "mode": "demo",
    "wouldHave": "POST /api/v1/changesets",
    "method": "POST",
    "path": "/api/v1/changesets",
    "detail": "Demo mode: this request was accepted and had no effect. ..."
  }
}
```

HTTP 200, not 4xx: the request was understood and accepted as far as demo
mode goes, and the only thing wrong with it is that there is no cluster to
apply it to. Every response a demo daemon produces — mutating or not —
carries `X-Vnprox-Demo: 1`.

This is a middleware in front of routing, not a check inside each handler.
A per-handler check is a promise about the handlers that exist today; a
middleware is a statement about the surface, so a route added next month is
covered by someone who never read this file.

**The one exception is the session lifecycle** (`POST /auth/login`,
`/auth/logout`, `/auth/oidc/callback`). A demo whose login POST answered "I
would have logged you in" would then show no screens at all.

**A consequence worth knowing:** several read surfaces in this API are
POST-shaped — `POST /simulate/path`, `POST /diagnose`, and the MCP transport
— and they are intercepted too. Those screens answer "would have" in a demo.
That is a real cost, taken deliberately: the acceptance criterion is "every
mutating API", and an allowlist of "POSTs that are really reads" is a list
someone has to keep correct forever with a store checksum as the only thing
standing behind it. Revisiting it is a follow-up (`T-2801-followup-01`), not
a silent widening.

## Demo mode and real endpoints, in both directions

**A demo daemon cannot be started against a real PVE endpoint.**
`config.LoadDemo` refuses any config that sets any `[pve]` key —
`api_url`, `token_file`, or the `dev_ticket_*` trio — with
`config.ErrDemoRealEndpoint`, before a collector is built or a port is
bound. The whole section is covered rather than `api_url` alone: a config
that sets only `token_file` still declares an identity minted against a real
cluster, and a demo daemon reading it would either use it or silently ignore
it, and both are wrong.

```
$ vnproxd --demo --config /etc/vnprox/vnprox.toml
invalid config: demo mode cannot be enabled against a configured PVE
endpoint: /etc/vnprox/vnprox.toml sets [pve.api_url pve.token_file]; a demo
daemon runs against the embedded synthetic cluster and must not be given a
way to reach a real one — remove the [pve] section, or start without --demo
```

**A real endpoint cannot be configured while a demo is running.** The routes
that attach an external cluster — `/federation/clusters` and
`/k8s/clusters`, the two routes that take an `apiUrl` — are refused with
`403 demo_real_endpoint_refused` rather than answered with a "would have".
The distinction is the point: "I would have attached your production
cluster" is not a sentence a demo may say.

Routes that configure an outbound *delivery* target rather than a cluster —
`/webhooks`, `/alert-rules` — deliberately get the ordinary would-have
answer instead, which is truthful (nothing is stored, nothing is sent) and
keeps this error code meaning one specific thing.

## The banner

A persistent, non-dismissible banner on every screen, plus a distinct amber
accent colour (`html.demo` re-points the `--color-accent-*` alias layer, so
every accent-coloured control in the app re-tints without a component
change).

The SPA learns it is a demo from `GET /api/v1/health`, the API's only
unauthenticated route — which is what lets the banner render on the **login
screen**. A demo that only announces itself after login has already let
someone type credentials at what they believed was their own cluster.

## The dataset

`internal/demo/dataset/cluster.yaml` and `internal/demo/dataset/flows.yaml`,
embedded with `go:embed`. They are checked-in fixtures so they are
versioned, reviewable and usable as a test corpus — not generated at
runtime, because a generator's output is reviewed by nobody and changes
shape whenever its seed does.

`cluster.yaml` is an ordinary `internal/pvemock` cluster fixture, the same
schema `testdata/clusters/*.yaml` uses, so one description backs both the
PVE API surface and the host-level reads. It is derived from
`testdata/clusters/three-node-vlan.yaml` with deliberate imperfections
added — a cluster with nothing wrong renders empty Findings and Drift
screens. Every one is enumerated in the fixture's own `mess:` list.

`flows.yaml` is a flow corpus with **relative** timestamps
(`at_offset_sec`, seconds before the seed moment), replayed into
`flow.Service.Ingest` — the same entry point every real decoder uses — at
startup and every five minutes after. Absolute timestamps in a checked-in
fixture would render an empty Flow Explorer the day after they were written.

Loading it from other code:

```go
ds, err := demo.LoadDataset()   // ds.Fixture (*pvemock.Fixture), ds.Flows
m, err := demo.New(logger)      // + the in-process server, transport, host reader
```

## The hosted read-only demo (T-2802)

```
vnproxd --demo --public-demo
```

`--public-demo` requires `--demo` and is refused without it: a read-only
façade in front of a daemon that still holds real PVE credentials is a
worse thing than no façade at all. Demo mode is what makes "there is
nothing real behind this" true; the edge is what makes "and you cannot
write to it" true.

### Every write is refused at the edge

`internal/publicdemo` wraps the entire daemon handler — in front of
routing, in front of authentication, in front of demo mode's own
middleware. A request whose method is not `GET`, `HEAD` or `OPTIONS` gets:

```
403 Forbidden
X-Vnprox-Public-Demo: 1
X-Vnprox-Public-Demo-Refused: public_demo_read_only
{"error":{"code":"public_demo_read_only","message":"this is a public, read-only vnprox demo: ..."}}
```

`X-Vnprox-Public-Demo` is on every response, refused or not.
`X-Vnprox-Public-Demo-Refused` is only on the ones the edge produced
itself — which is what lets a test tell "the edge refused this" apart from
"the daemon answered 403 for its own reasons".

**The classification is the method and nothing else.** There is no
allowlist of "POSTs that are really reads", and that is a deliberate
tightening of T-2801's position rather than an oversight: the only thing
standing behind such a list at a public edge is somebody's continued
correctness about which of ~215 routes are safe, and one wrong entry is a
stranger writing to the instance. The cost is stated in the gap list
below.

**Not even `POST /auth/login`.** A public demo has no login screen. The
edge mints a session per visitor by driving the daemon's own login handler
in-process with the fixture's built-in superuser, so a visitor's session is
indistinguishable from an operator's — same route, same audit entry — while
the session cookie never leaves the server. An inbound session cookie is
stripped before forwarding, so a visitor cannot present a session the edge
did not mint.

### One visitor, one everything

A visitor is an opaque `vnprox_demo_visitor` cookie: `HttpOnly`, `Secure`,
`SameSite=Strict`. Each one gets its own daemon session, its own request
budget, and its own scratch state.

Because the daemon's `/layouts` routes are refused like every other write,
the edge serves a visitor-scoped scratch surface of its own:

```
GET /demo/visitor/session          who am I, and what are my limits
GET /demo/visitor/state/{key}      read one scratch value
PUT /demo/visitor/state/{key}      write one scratch value
```

It is deliberately **not** in `docs/openapi.json`: it is not the product's
API, nothing under it reaches the daemon or its store, it is held in memory,
and it is discarded when the visitor goes idle. The SPA persists the tour's
progress and the map's layout through it (`web/src/tour/`), which is what
makes one visitor's layout invisible to another. A normal daemon serves no
such route, and the 404 it answers is exactly how the SPA learns it is not
in a public demo — no config flag, so no daemon without an edge can get it
wrong.

### Caps

Every cap is per-visitor except the visitor count, which is the point: a
cap that took the instance down for everyone is the failure they exist to
prevent.

| Cap | Default | Exceeded |
|---|---|---|
| Requests per visitor | 120 burst, +1 every 500 ms | `429 public_demo_rate_limited`, that visitor only |
| Scratch state per visitor | 256 KiB, 32 keys | `413 public_demo_state_too_large`; nothing already stored is disturbed |
| Visitors | 200 | `503 public_demo_at_capacity` for the **arriving** visitor; nobody seated is evicted |
| Idle visitor | 30 min | Reclaimed only when a new arrival needs the room |

### The tour

`web/src/tour/tourScript.ts` is a script — data, not components — covering
six surfaces of `docs/datasheet.md`, one from each thing the datasheet
claims vnprox does: the topology map, physical discovery, the findings
stream, the flow explorer, the SDN cockpit, and history. It is resumable
(progress lives in the visitor's own scratch state, so a reload finds it)
and skippable (a skipped step is recorded as skipped, never as completed).

## Known gaps

- **There is no hosted instance.** T-2802's first bullet is "a public
  instance serving demo mode", and this repository has no domain, no object
  storage, no deploy target and no CI budget to deploy from — the same wall
  `T-2803` recorded rather than papering over. Everything such an instance
  would need in order to be safe is built and tested here; the instance is
  not. Concretely unmet: nothing at a public URL, no TLS certificate for a
  public name, no rate limiting or abuse handling in front of the process,
  and no operational story for restarting it.
- **A public instance's login limiter needs configuring, and there is no
  supported knob.** The edge mints one session per visitor through the real
  login route, and `internal/auth`'s limiter is keyed `(IP, username)` with
  a default of 10 attempts refilling one per 30 s. Every visitor shares the
  demo's one username, so visitors behind one NAT would throttle each
  other. `testdata/demo-public.toml` raises it with the dev-only
  `dev_login_rate_*` keys, which is fine for the e2e stack and not fine for
  a public instance. Fixing it properly means either a supported `[server]`
  login-rate setting or a mint path that does not go through the login
  handler — both widen the authentication surface, and neither is worth
  doing speculatively for an instance that does not exist.
- **Partially resolved 2026-08-18 (`T-2801-followup-01`): the path
  simulator and the diagnosis ladder now work in plain `vnproxd --demo`
  (no `--public-demo`).** `POST /simulate/path` and `POST /diagnose` are
  read surfaces with mutating methods — `demo.go`'s `demoReadOnlyPosts`
  lets exactly these two execute for real instead of answering "would
  have", after reading each handler end to end (not guessing from the
  route name) to confirm neither writes to the store: `handleSimulatePath`
  has no store handle reachable at all; `handleDiagnose` normally appends
  `diagnose.run`/`diagnose.step` audit rows, so `router.go` wires that
  dependency to `nil` in demo mode and reuses the nil-audit no-op path
  every other optional audit seam in this package already has.
  `TestDemoMode_ReadOnlyPostsExecuteForReal` (`internal/api/demo_test.go`)
  asserts the store checksum is unchanged across both calls, with the same
  control-leg discipline as AC2's changeset-lifecycle test.
  **`internal/publicdemo`'s hosted edge is deliberately unchanged and still
  refuses both** — a conscious decision, re-confirmed on the record when
  T-3303 stood up `demo.vnprox.com`: that edge's whole design is "no
  semantic allowlist, ever, because the only thing standing behind one is
  somebody's continued correctness, and one wrong entry is a stranger
  writing to the instance" (`internal/publicdemo/doc.go`). This app-level
  fix does not weigh against that — `Edge.ServeHTTP` refuses a mutating
  method before the request ever reaches `demoWriteMiddleware`, so the two
  layers don't interact. Net effect: someone running `vnproxd --demo` on
  their own machine (`docs/install.md`'s "try it with no install" path) now
  gets a real path simulator and diagnosis ladder; a visitor on the hosted
  public instance still doesn't, and the tour still routes around both
  there. If that tradeoff is ever revisited, it is a `internal/publicdemo`
  decision to make deliberately, not a side effect of this fix.
- The MCP transport is unreachable in a public demo for the same reason.
  That one is not a loss: an unauthenticated public MCP endpoint is not
  something to want.
- A demo daemon reports a handful of findings about its own host rather than
  the synthetic cluster — `cert_missing`, `cert_unreadable`,
  `peer_trust_degraded` — because it has no real cluster CA to find. They
  are honest and, since 2026-08-18 (T-3303's `resolveCertsRoot`), the SAME
  on every machine: this was written assuming "because `/etc/pve` does not
  exist off a Proxmox node", which held on every dev/CI machine this was
  built and tested on, and broke the first time a demo daemon actually ran
  on a real PVE host (pve001) — nothing had gated the cert scanner's root
  path on demo mode, so it scanned pve001's real pmxcfs and these findings
  briefly named real node names (`pvecube`, `pve001`) instead of reporting
  "no cluster CA" about a nonexistent one. Fixed by pointing a demo
  daemon's cert-scan root at a guaranteed-absent path instead of relying on
  the host happening not to have one; `cmd/vnproxd/server.go`'s
  `resolveCertsRoot` doc comment has the full account. They read as "this
  product is broken" on a demo screen either way. Not suppressed here,
  because suppressing findings is exactly the kind of thing that should not
  be done quietly.
- The fixture's `corosync:` ring status is data nothing consumes yet: the
  `corosync_link_degraded` check reads `corosync.conf` from pmxcfs, which a
  demo has no equivalent of.
