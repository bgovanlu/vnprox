# T-3303 finding: demo mode's cluster isolation assumed a non-PVE host

Found live, standing up `demo.vnprox.com` on pve001 (a real PVE 9.2.10 node) — the first time
demo mode has ever run anywhere `/etc/pve` genuinely exists. Every prior dev/CI machine this was
built and tested on lacked it, so a class of bug in demo mode's isolation guarantee was
unreachable until now.

## What broke, and what's fixed

`internal/certs.Service` scans `certs.DefaultRoot` (`/etc/pve`) for cluster certificates,
unconditionally — nothing gated its `Root` on `cfg.Demo`. `docs/features/demo-mode.md`'s own
"known gaps" section already expected `cert_missing`/`cert_unreadable` findings to fire in demo
mode, reasoned as harmless because "`/etc/pve` does not exist off a Proxmox node." That reasoning
was correct everywhere it was checked and wrong here: pve001's `/etc/pve` is real, and the demo
instance's first run scanned it and reported real node names (`pvecube`, `pve001`) in a
supposedly fully-synthetic public demo's findings feed — a real information leak, even though the
topology/guest/bridge data itself stayed fully synthetic throughout (confirmed via
`GET /health`'s collector node name, `pve1` — the fixture's own name, never the real one).

Fixed: `cmd/vnproxd/server.go`'s `resolveCertsRoot` points a demo daemon's cert-scan root at a
guaranteed-absent path (under its own isolated state dir) instead of relying on the host not
having a real `/etc/pve`. Verified live on pve001 after the fix: the same finding now reads
`cert:cert_unreadable||/var/lib/vnprox-demo-public/no-real-pve-in-demo-mode/pve-root-ca.pem` —
synthetic, absent, honest. Regression test: `cmd/vnproxd/demo_test.go`'s
`TestResolveCertsRoot_DemoModeAvoidsARealPmxcfs`.

## What's NOT fixed at the code level — contained by deployment, not by demo mode itself

The same live run surfaced the same *class* of gap in at least three more places, none of them
data leaks in practice **today**, all of them relying on the deployment's permission model rather
than on `cfg.Demo` being checked in code:

| Subsystem | What it read | Why it didn't leak here |
|---|---|---|
| `internal/peer/trust.go` | `/etc/pve/pve-root-ca.pem` (cluster CA, for peer-API TLS pinning) | `permission denied` — `vnprox-demo` is not in the `www-data` group `/etc/pve`'s files (`root:www-data 0640`) require |
| `internal/host` (corosync reading, `internal/host/corosync.go`) | `/etc/pve/corosync.conf` | same permission denial; falls back to management-IP-only detection, logged, not silent |
| `internal/findings/health_corosync.go` | executes `corosync-cfgtool -s` | exits 1 off a real corosync context this user can't reach; the finding this feeds degrades, doesn't fabricate data |

`grep -rl '"/etc/pve' internal/ cmd/` also turns up `internal/change/protected.go`,
`internal/change/apply_snapshot.go`, `internal/backup/bundle.go`, `internal/doctor/checks.go`,
`internal/verify/checks_host.go`, `internal/config/config.go` — not all individually confirmed
safe or unsafe in demo mode; the certs bug above is the only one actually observed emitting real
data, but the pattern (a path/exec call with no `cfg.Demo` check anywhere near it) is repeated
enough that "audited every one" is not a claim this report makes.

**The actual boundary holding today is `packaging/systemd/vnprox-demo-public.service`'s
`User=vnprox-demo`/`Group=vnprox-demo`** — a plain system user in no PVE-relevant group, so every
one of the reads above fails closed by Unix permissions before it ever reaches vnprox's own logic.
This is real containment, not a theoretical one (verified: every attempt above logs
`permission denied`, not data) — but it is an operational control, not a code-level guarantee the
way `resolveCertsRoot` now is for certs. **Do not run this service, or any future public-demo
instance, as `root` or as a member of `www-data`/any PVE-privileged group** — that would silently
remove this boundary and reopen the exact leak the certs bug was, for these other subsystems.

## Recommended follow-up (not done here — scope, not this session's remaining time)

A proper fix mirrors `resolveCertsRoot`'s shape for each of the paths above: either gate the read
on `cfg.Demo` at its call site, or (better, fewer call sites to keep correct forever) give
`internal/demo.Mode` a host-reader seam the same way it already has one for the PVE HTTP
transport (`internal/demo/transport.go`), so a demo daemon structurally cannot open a real
`/etc/pve` path regardless of which subsystem asks. Filed here rather than fixed piecemeal because
the second shape is the right one and is a real design task, not a five-line patch per call site.
