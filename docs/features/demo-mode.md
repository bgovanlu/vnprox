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

## Known gaps

- A demo daemon reports a handful of findings about its own host rather than
  the synthetic cluster — `cert_missing`, `cert_unreadable`,
  `peer_trust_degraded` — because `/etc/pve` does not exist off a Proxmox
  node. They are honest (the daemon really has no cluster CA) but they read
  as "this product is broken" on a demo screen. Not suppressed here, because
  suppressing findings is exactly the kind of thing that should not be done
  quietly.
- The fixture's `corosync:` ring status is data nothing consumes yet: the
  `corosync_link_degraded` check reads `corosync.conf` from pmxcfs, which a
  demo has no equivalent of.
