# Needs hardware validation

Behaviors developed and tested only against `internal/pvemock` that a real Proxmox VE cluster must
confirm before v1.0 ships. Per CLAUDE.md, implementation agents have no live PVE access; this is
the accumulating checklist for the first hardware pass (owner: T-6xx hardening/validation work).
Check items off with the PVE version tested.

## PVE API behavior

- [ ] **API-token auth**: real PVE accepts `Authorization: PVEAPIToken=user@realm!tokenid=secret`
      as implemented in `internal/pve` (header shape, no cookie/CSRF), and **token privilege
      separation** (`privsep`) semantics — pvemock models tokens as carrying the owner's full
      privileges (`internal/pvemock`, TokenSpec).
- [ ] **Ticket-as-password renewal**: PVE accepts a still-valid ticket as the `password` on
      `POST /access/ticket` (the client drops the plaintext password after the first successful
      ticket renewal — `internal/pve/auth.go`); confirm the acceptance window near expiry and
      behavior on TFA-enabled realms.
- [ ] **`GET /access/permissions` response shape**: real per-path ACL tree with concrete privilege
      enumeration (pvemock reports a flat list at `/` and may carry a literal `"*"`);
      `BuildCapabilities` (`internal/auth/caps.go`) handles both, but the real shape should be
      captured as a fixture.
- [ ] **TFA/TOTP flow**: modern PVE returns a two-step NeedTFA ticket-challenge; the mock (and
      `POST /auth/login`'s `otp` passthrough) model the single-step `otp` param variant only.
- [ ] **IPAM wire shapes**: exact fields/types of `GET /cluster/sdn/ipams` and
      `…/{ipam}/status` (notably `gateway` as 0/1 int, `vmid` typing), and behavior with
      NetBox/phpIPAM plugins vs the built-in `pve` IPAM.
- [ ] **PUT request encoding**: the client sends JSON bodies; pvemock and modern PVE accept this,
      but confirm against the oldest supported PVE version.
- [ ] **Ticket expiry**: real tickets expire (~2h); confirm the renewal margin
      (`Config.TicketRenewAfter` default) beats it comfortably under clock skew.

## Peer API (T-301)

- [ ] **Peer TLS trust**: real peer daemons present the node's PVE
      certificate (docs/architecture.md §9), typically chained to
      `/etc/pve/pve-root-ca.pem`; `internal/peer.Client` does not yet pin
      that CA (it inherits `net/http`'s default trust store unless a caller
      supplies `ClientOptions.HTTPClient`). Confirm the right pinning
      strategy against a real cluster before T-303/T-304 rely on it beyond
      the plain-HTTP test harness this task used.
- [ ] **`/etc/pve/vnprox/cluster.secret` under pmxcfs**: `SecretStore`'s
      generate-if-absent + `os.Link`-based atomic publish + mtime-poll
      `Watch` have only run against a real filesystem in temp dirs — pmxcfs
      is a FUSE filesystem with its own semantics for permissions/hard
      links/rename; confirm `os.Link` (used to avoid a torn-write race, see
      `planning/reports/T-301.md` §3) actually works on pmxcfs rather than
      silently failing or behaving like a copy.

## Distributed rollback / local-timer protocol (T-304)

- [ ] **Whole-second HMAC replay collisions under real timing**: `internal/peer`'s replay cache
      keys on the exact signed request (method, path, body, whole-second timestamp). T-304's
      testing surfaced that two genuinely-distinct requests to the same peer node with identical
      bodies (e.g. `POST /api/peer/host/ifreload {"node":"pve2"}` issued once during apply and
      again moments later during a mid-apply rollback of that same node) sign identically and
      collide if they land in the same wall-clock second — the test harness works around this
      with an auto-ticking fake clock (`internal/change/distributed_test.go`'s `clock()`), which
      real time also provides in practice, but the actual gap between two such calls on a fast
      LAN has not been measured against real hardware. If this proves to matter in practice, the
      fix belongs in `internal/peer`'s signing/replay scheme (out of T-304's scope — see its
      report's deviation notes), not in `internal/change`.
- [ ] **`ClusterNodeAgent`/`ClusterTimerAgent` PVE cluster-status discovery timing**: production
      wiring (`cmd/vnproxd/server.go`) resolves this daemon's own node name from
      `collect.Collector.Status().LocalNode`, which is empty until the first successful PVE
      cluster-status poll — confirm the real-world window between daemon startup and that first
      poll succeeding doesn't leave a coordinator unable to recognize its own node during that
      gap on a real cluster.
- [ ] **Real elapsed-time behavior of the per-node local timer across an actual `ifreload`**:
      `LocalTimerAgent`'s restore-on-fire path (`internal/change/localtimer.go`) reuses
      `NodeAgent.StageInterfaces`/`ReloadInterfaces`, the same host-writer T-205 already flagged
      as unvalidated against real ifupdown2 — T-304 adds no new host-level operation, but doubles
      the real-hardware surface that flag covers (a mid-apply rollback and a confirm-timeout
      rollback can now both invoke it, from two different daemons, on the same node).

## Host / OS behavior

- [ ] **`systemctl start vnprox` from the .deb** on a real PVE node (the container test script
      cannot run systemd as PID 1 — `packaging/test/deb-install.sh` documents the gap).
- [ ] **Real netlink/LLDP/bonding readers** on a PVE node with bonds, VLAN-aware bridges, and
      lldpd running (`internal/host` integration tests skip without privileges/peers;
      `TestReal_LLDP` and bond-detail tests have never run against real hardware).
- [ ] **PVE-cert reuse + hot-reload** against a real pveproxy certificate rotation.

## UI

- [ ] **60fps pan/zoom measurement on a GPU-composited dev machine**: the committed measurement
      (`docs/testing/topology-performance.md`) is from a headless software-rasterized VM
      (~35 fps, with an idle control proving the environment itself hits 60) — a pessimistic
      floor, not a pass/fail verdict. A console-paste rAF snippet is included in the doc.
      (The four-layer render itself IS regression-protected: `npm run e2e` runs a Playwright
      screenshot-baseline test with real login — see
      `docs/testing/topology-render-verification.md`.)
- [ ] **`host-interfaces` raw source in the inspector against a real node**: the captured detail
      fixture's `pve-network` raw source is live-captured, but the interfaces(5) stanza half was
      hand-extended per the pinned shape (`web/src/topology/__fixtures__/inventory-detail-vmbr0.json`,
      noted in its test header).
