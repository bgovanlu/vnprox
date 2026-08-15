# Needs hardware validation

Behaviors developed and tested only against `internal/pvemock` that a real Proxmox VE cluster must
confirm before v1.0 ships. Per CLAUDE.md, implementation agents have no live PVE access; this is
the accumulating checklist for the first hardware pass (owner: T-6xx hardening/validation work).
Check items off with the PVE version tested.

> **Much of this list is now executable (T-2501).** `vnproxctl verify --suite=hardware` runs 26
> checks across every feature area the matrix marks `B` or `V`, decides pass/fail/skip itself, and
> writes a signed report carrying the evidence each verdict rests on
> (`vnproxctl verify --list` shows what each one needs; see `docs/deployment.md`). It replaces the
> read-a-line-and-write-down-what-happened loop for the behaviours it covers — the items below stay
> because they are the ones a command still cannot decide, and because an item is only ticked here
> when a human returned real output.
>
> The suite **refuses to run against `internal/pvemock`** without `--allow-mock`, and a run in
> which every check skipped exits non-zero reporting `0 passed`. Both exist so a green run cannot
> be produced by accident and filed here.

## Deploy-time validation, 2026-08-05 (pvecube, pve-manager/9.2.4, kernel 7.0.14-4-pve, single node)

Obtained while deploying this arc's merged work, not through T-1801's harness. Single node, so
nothing cross-node is covered.

- [x] **`/etc/pve/pve-root-ca.pem` exists at the documented path and loads as a trust anchor**
      (T-1906). Daemon logged `peer: cluster CA trust anchor loaded; peer TLS is pinned to it`.
      `openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem` → OK.
- [x] **The peer leaf certificate's SAN list does NOT necessarily cover the node's actual address**
      — and this is a **failure**, filed as `T-1906-bug-01`. This node's only address is
      `192.168.1.9` (vmbr0); its `pve-ssl.pem` carries `IP 192.168.100.99` (stale), plus
      `DNS pvecube`/`pvecube.localdomain`. A peer dialled by IP would fail pinned hostname
      verification. Corrects the assumption in T-1906's report that the open question was
      "hostname-only SANs" — the real hazard is a *stale* IP SAN.
- [x] **The forward-only migration chain runs on a real store with real data** (T-1807): an
      in-place `apt install` upgraded schema 32 → 34 (T-1805's 33 and T-2003's 34) against a
      3.7 MB production store, service came back active, all three collectors reporting success.
- [x] **`vnproxctl backup` works against a real store** (T-1901): wrote a 637.6 KiB archive,
      schema 34, 3 entries, correctly reporting that no key material was included.
- [x] **`vnproxctl support-bundle` leaks no real credential** (T-1902). Built from this live
      install and scanned decompressed: the session key (first 16 bytes, base64) and the PVE API
      token tail are both absent. **Scan validated by a control first** — the same scan finds
      `pvecube` in the bundle, so the negatives mean something. This is stronger evidence than
      the fixture-based tests, because the credentials were real.
- [ ] **Still not covered on hardware**: anything cross-node (peer API round trips, pmxcfs
      replication, distributed rollback, HA lease fencing), and `T-1906-bug-01`'s actual failure
      mode, which needs a second node to observe rather than infer.

## Deploy-time validation, 2026-08-07 (pvecube, same node) — `vnproxctl doctor` (T-1904)

Obtained by running the new command on the real node immediately after `apt install`. Result:
**6 passed, 0 warned, 0 failed, 4 skipped, exit 0.** Each pass below is a check whose logic had
only ever been exercised against fakes.

- [x] **`pmxcfs` check reads a real `/etc/pve`** — passes against the live pmxcfs mount, not a
      fixture directory.
- [x] **`schema_version` reads a real store** — reported "database schema 34 matches the binary"
      against the production SQLite file, via `store.InspectSchemaVersion` with the daemon
      running. Confirms the read is safe against a live, locked store.
- [x] **`port_conflict` correctly recognises vnprox itself** — reported "port 8007 is held by
      vnprox itself" rather than a conflict. This is the branch that distinguishes a real
      collision from the normal post-install state, and it depends on `ss -tlnpH` output parsing
      that no fixture can prove. **Validated on the one host where getting it wrong would make
      doctor cry wolf on every healthy install.**
- [x] **`key_files` against real permissions** — `/etc/vnprox/keys/session.key` and the PVE token
      file both present at 0600 as the packaging intends.
- [x] **`disk_headroom` against a real filesystem** — `syscall.Statfs` path, including the
      walk-up-to-an-existing-ancestor logic for the not-yet-created capture directory.
- [x] **`config` against the packaged config** — parses `/etc/vnprox/vnprox.toml` as installed.

**A defect in doctor itself, found by this deploy and fixed the same day.** The four skipped
checks originally printed *"no PVE credentials configured yet (expected before first setup — run
vnprox-setup)"*. pvecube **is** fully set up and its collectors were polling PVE successfully at
that moment, so the message was a confident diagnosis of a condition doctor had never checked, and
it was false. A `skip` means "not checked"; asserting a cause turns it into an unverified claim —
the exact failure `StatusSkip` exists to prevent. Reworded to state what was not checked and what
to use instead, with `TestSkipReasonsDoNotDiagnose` (proven by mutation) to stop it recurring.

- [ ] **Still not covered**: the four skipped checks themselves (`pve_reachable`,
      `pve_privileges`, `peer_secret`, `clock_skew`) — they need the daemon's authenticated
      clients, which is `T-1904-followup-02`. `peer_secret` additionally needs a second node.

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

- [~] **Peer TLS trust — pinning implemented (T-1906), the real chain's shape
      still unverified.** The original item read "`internal/peer.Client` does
      not yet pin that CA (it inherits `net/http`'s default trust store)".
      That is fixed: `internal/peer.Trust` pins `/etc/pve/pve-root-ca.pem`
      (`[peer] ca_file`) as the sole anchor, never consults the system pool,
      fails closed with no anchor, re-reads on a 30 s cadence for rotation,
      and classifies a verification failure as `peer_untrusted` rather than
      `peer_unreachable` (see `planning/reports/T-1906.md`). Every test CA and
      certificate is built in-process with `crypto/x509`, so what remains
      genuinely unknown is the **real chain's shape**, and that still needs a
      cluster. **T-1801's harness (`planning/validation/`) does not exist
      yet**, so these live here, phrased as harness steps ready to be lifted
      into it verbatim when it lands. Capture on hardware:
      - [ ] The **actual certificate chain** `pveproxy`/vnproxd serves on
            8007 on a real node: is the leaf (`/etc/pve/local/pve-ssl.pem`)
            issued *directly* by `pve-root-ca.pem`, or is there an
            intermediate? Does the served chain include the issuer, or only
            the leaf? Pinning a root works either way only if the peer sends
            enough of the chain — capture it (`openssl s_client -connect
            <node>:8007 -showcerts`) and keep it as a fixture.
      - [ ] The leaf's **SANs**: peers are dialled by IP
            (`https://<node-ip>:8007`, `Client.Peers` builds the address from
            `GET /cluster/status`). Confirm PVE's generated certificate
            carries the node's management **IP** in its SAN list and not only
            the hostname. If it is hostname-only, hostname verification will
            fail on every peer and the client needs an explicit
            `ServerName`/name-resolution step — this is the single most
            likely way pinning breaks on iron, and it fails *closed*, so it
            would present as every peer becoming `peer_untrusted` at once.
      - [ ] Behaviour with a **custom certificate** installed
            (`pveproxy-ssl.pem`, e.g. a Let's Encrypt / enterprise-CA cert):
            such a node's peer certificate is *not* issued by
            `pve-root-ca.pem`, so pinning will reject it. Confirm and
            document the intended posture (`[peer] ca_file` pointed at the
            operator's own CA bundle is the designed answer).
      - [ ] **Rotation on iron**: `pvecm updatecerts -f`, then confirm peers
            recover within one reload interval with no daemon restart, and
            that the WARN/INFO log transitions look right.
      - [ ] `/etc/pve/pve-root-ca.pem` **availability** during a
            `pve-cluster` restart: how long it is absent, and that the
            last-known-good behaviour (keep verifying against the previously
            loaded anchor, WARN) is what actually happens rather than a
            fail-closed blip.
      - [ ] **Mixed-version rollout**: a node still running a pre-T-1906
            build serving traffic to a pinned peer — the pinned side is the
            client, so this should be unaffected, but confirm no asymmetry.
- [x] **`/etc/pve/priv/vnprox/cluster.secret` under pmxcfs (T-608, validated
      2026-07-12 against a real PVE 9.2.4 node, "pvecube")**: found two real
      bugs, both fixed. (1) pmxcfs rejects `link(2)` outright with `EPERM`
      everywhere — `SecretStore.generateSecretFile`'s `os.Link`-based atomic
      publish (the mechanism `planning/reports/T-301.md` §3 describes) would
      have failed on every single real-hardware secret-generation attempt,
      not just raced unsafely; switched to `os.Rename`, which pmxcfs
      supports and which is atomic on a given filesystem (see
      `internal/peer/secret.go`'s updated `generateSecretFile` comment for
      the concurrent-generation tradeoff this implies). (2) pmxcfs only
      auto-restricts files to `0600 root-only` under `/etc/pve/priv/` — it
      silently coerces creation-time mode to `0640 root:www-data` (and
      rejects `chmod()` outright) everywhere else under `/etc/pve`, so the
      secret's default path moved from `/etc/pve/vnprox/cluster.secret` to
      `/etc/pve/priv/vnprox/cluster.secret` (`internal/peer.DefaultSecretPath`,
      `packaging/bin/vnprox-setup`, `packaging/debian/postrm`, and the docs
      referencing it were all updated to match). Not yet validated: real
      cross-node pmxcfs replication (this was a single-node cluster).

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
- [ ] **WireGuard changeset apply under `ProtectSystem=strict` (v3.0.2).** `cmd/vnproxd/wireguard.go`'s
      `hostWGGateway` writes each tunnel's wg-quick config under `/etc/wireguard` (`MkdirAll` 0700 +
      `WriteFile` 0600). v3.0.2 adds `/etc/wireguard` to the unit's `ReadWritePaths` and creates the
      directory in `postinst` (0700 root:root) so the sandbox bind target always exists. Confirmed by
      code inspection only — a real WireGuard apply on a hardened node (secret sealed, `wg`/`wg-quick`
      present, tunnel brought up) has never run against real hardware. Root cause was inferred from the
      identical v3.0.1 keys crash, not reproduced. Validate that a WG apply now succeeds *and* that a
      node with no `/etc/wireguard` and no wireguard-tools still starts the unit (the bind target is
      postinst-created, so `ReadWritePaths` should not fail unit start).
- [ ] **Is `/etc/pve` (pmxcfs FUSE) even read-only under `ProtectSystem=strict`? (v3.0.2).** The cluster
      secret's fallback generate-if-absent write targets `/etc/pve/priv/vnprox/cluster.secret`
      (`internal/peer.DefaultSecretPath`), which is normally pre-seeded by `vnprox-setup` (so the daemon
      only reads it in practice). It is deliberately **not** in `ReadWritePaths` — bind-mounting a FUSE
      submount RW under a sandbox is dubious, and it's unconfirmed whether systemd's `ProtectSystem=strict`
      remount even makes a pmxcfs FUSE mount read-only in the service namespace. Validate on a real node:
      does the fallback secret-generation path (delete the secret, restart the daemon) work or hit
      `read-only file system` under the hardened unit? If it fails, the fix is to ensure `vnprox-setup`/
      `postinst` always pre-seeds it (not to widen the sandbox onto pmxcfs).
- [ ] **Real netlink/LLDP/bonding readers** on a PVE node with bonds, VLAN-aware bridges, and
      lldpd running (`internal/host` integration tests skip without privileges/peers;
      `TestReal_LLDP` and bond-detail tests have never run against real hardware).
- [ ] **PVE-cert reuse + hot-reload** against a real pveproxy certificate rotation.
- [ ] **LACP actor/partner detail parsing (T-804)** against a real 802.3ad bond on a live switch:
      the exact `/proc/net/bonding/<name>` "details actor lacp pdu:"/"details partner lacp pdu:"
      block format (field names/indentation/presence) has only been checked against this task's
      own hand-written golden fixtures (`internal/host/bonding_test.go`), not a real kernel's
      output, and may vary across bonding driver/kernel versions vnprox targets (docs/architecture.md
      §10 D9: PVE 8.2+/9.x). Also unverified: netlink's per-slave
      `IFLA_BOND_SLAVE_AD_ACTOR_OPER_PORT_STATE`/`IFLA_BOND_SLAVE_AD_PARTNER_OPER_PORT_STATE`
      attribute availability/behavior on a real running 802.3ad aggregator
      (`internal/host/netlink_linux.go`'s `applyBondADState` — best-effort, /proc remains the
      primary source since `github.com/vishvananda/netlink` v1.3.1's bond-level `IFLA_BOND_AD_INFO`
      parsing is an explicit upstream TODO stub, so actor/partner system ID/key are /proc-only
      regardless). A genuine split-brain/desynced-slave scenario (this task's fixtures simulate
      both) should also be reproduced against a real switch to confirm `lacp_partner_mismatch`
      fires as designed.

## Management-redundancy wizard (T-703)

These are the T-703 acceptance-criterion-7 items — nothing about restructuring an *active*
management path can be proven against `internal/pvemock` (node-file network ops write the dev host
sandbox and never touch pvemock's PVE network model — docs/architecture.md §4's T-607 correction —
so the applied change never re-enters the inventory the `mgmt_single_path` finding is computed
from; and pvemock does not model an `ifreload` outage at all):

- [ ] **Real `ifreload -a` outage window while restructuring the *active* management bridge**
      (flow A bonding the live mgmt uplink; flow C moving the address to a new VLAN sub-interface):
      length/character of the transient loss, and whether the browser's commit-confirm round-trip
      survives it.
- [ ] **`mgmt_single_path` finding actually clears after a real apply**: on a real node the applied
      bond/VLAN re-enters netlink and the collector's inventory, so the finding should clear on the
      next poll — the mock cannot show this (the e2e asserts the apply lifecycle + audit ack
      instead, `web/e2e/mgmt-redundancy.spec.ts`), and neither can it show "the fixture interfaces
      file shows the bond" reaching the topology.
- [ ] **LACP (802.3ad) bond formation against a real switch** with the two ports configured for
      LACP first, and that **`active-backup`** (the wizard's default when LLDP can't verify a LACP
      peer) fails over cleanly on a cable pull.
- [ ] **Auto-rollback with management actually down**, especially when the changeset targets a
      *peer* node's management path (T-304 local-timer machinery under a real partition) — the
      unit test (`TestMgmtWizard_FlowA_AutoRollback`) proves the rollback restores a byte-identical
      pre-state via the deterministic fake timer, but not that connectivity is genuinely regained
      when the mgmt link was down for real.
- [ ] **Flow C protected-set refresh against real corosync/hosts state**: that moving the mgmt
      address to a new VLAN carrier and then `PUT /protected-interfaces` (from
      `GET /protected-interfaces/suggest`) leaves corosync/pveproxy reachability intact — vnprox
      keeps re-addressing out of scope by construction, but the carrier *move* should be confirmed
      not to perturb corosync's own ring binding on a real cluster.

## SDN subnet gateway (T-701)

- [ ] **Exact real-PVE (8.2/9.x) rejection point/message for SNAT-without-gateway and
      gateway-outside-CIDR**: `internal/pvemock/sdn.go`'s `subnetGatewayError` rejects both shapes
      with a 400 at `POST`/`PUT .../subnets` — a plausible, clearly-flagged approximation of PVE's
      SubnetPlugin behavior (T-701 root-cause analysis §4), not a verified mirror of it. Confirm
      both are rejected at subnet stage time (not deferred to `PUT /cluster/sdn` apply) and capture
      the real error text/PVE version.
- [ ] **Whether PVE registers the gateway's IPAM record at subnet create/update, or only at SDN
      apply**: `internal/pvemock/ipam.go`'s `registerSubnetGateway` takes the simpler, testable
      position that the `gateway: true` record exists (and is refreshed) as soon as the subnet
      does, matching how `three-node-vlan.yaml`/`evpn-lab.yaml`/`ipam-lab.yaml` already hand-model
      their own gateway records — pvemock fidelity work here depends on which is actually true.
- [ ] **Whether `GET /cluster/sdn/ipams` lists a built-in `pve` IPAM plugin by default when the
      cluster has never explicitly configured one**: `internal/pvemock/ipam.go`'s
      `effectiveIpams`/`defaultIpamID` synthesize a `{id: "pve", type: "pve"}` entry when the
      fixture declares zero (needed for `single-node.yaml`'s zone/vnet/subnet writes to have
      somewhere to register a gateway record at all) — confirm whether real PVE's built-in IPAM is
      reachable this way with zero `/etc/pve/sdn/ipams.cfg` entries, or whether a zone must
      explicitly set `ipam: pve` first.
- [ ] **EVPN anycast-gateway realization when the gateway is absent**: does the zone's per-node
      status (`GET /cluster/sdn/zones/{zone}/status`) report an error, or is routed/exit-node
      traffic simply dark with no observable signal at all? This determines whether
      `sdn.evpn_gateway_missing` (`internal/change/validate_advisory.go`) is the *only* signal an
      operator gets, or whether T-402's post-apply zone health check would also eventually catch
      it.
- [ ] **Whether simple-zone SNAT additionally depends on `net.ipv4.ip_forward`** being set by PVE
      on the zone's member nodes — if PVE doesn't set this itself, a subnet that passes every
      vnprox/PVE-side check above could still have non-functional SNAT for a host-level reason
      outside either's config surface.

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

## SDN object naming and VNI (issue #3 — inline validation)

- [ ] **Exact real-PVE SDN zone/vnet id charset and length cap.** vnprox now blocks
      characters outside `[A-Za-z][A-Za-z0-9]*` (charset only) end to end — inline in the
      guided wizards (`web/src/sdn/wizards/validation.ts`), in the change engine
      (`internal/change.schemaSDNName` / `schema.sdn_name_invalid`), and in pvemock
      (`internal/pvemock.sdnParamVerifyError`, returning a PVE-style "Parameter verification
      failed" 400). The **length** limit is intentionally only a non-blocking wizard warning
      (default 8 chars): existing golden fixtures/tests carry longer ids (`bypasszone`,
      `ghostzone`) and hyphenated ones (`dc-evpn`, `vnet-tenant-a`), so the exact cap and
      whether hyphens are ever accepted must be confirmed against a live PVE (8.x/9.x) before
      the length rule can be tightened to a hard error or the charset relaxed.
- [ ] **VNI required for vxlan/evpn vnets.** vnprox now errors on a vxlan/evpn vnet with tag 0
      (`internal/change.vniRequiredFindings` / `sdn.vni_required`) and the wizards require a VNI.
      Confirm real PVE rejects a tag-less vxlan/evpn vnet at stage time (expected) and the exact
      message.
- [ ] **Full VNI range.** The wizard and the change engine currently cap a vnet tag at 4094
      (`maxVID`), matching the existing schema-class range. Real VXLAN/EVPN VNIs go to 16777215;
      widening the whole stack (schema range + the `fixClampVID` clamp target) to the full range
      is a scoped follow-up, deferred here to avoid destabilizing the well-tested tag-clamp
      machinery without a live cluster to validate the boundary against.

## Guest-agent live path probes (T-802)

- [ ] **Exact in-guest probe command per guest OS family.** `internal/probe`'s `buildCommand`
      deliberately implements exactly one target profile — a Linux guest with iputils-ping
      (`ping -c 1 -W <secs> <ip>`) and netcat-openbsd (`nc -z -w <secs> <ip> <port>`) — rather than
      guessing a "portable" command across every guest OS/toolchain a real PVE cluster might run.
      Unverified against a real QEMU guest agent and real guest images: (1) whether the target
      Linux images vnprox actually needs to support (Debian/Ubuntu cloud images at minimum) ship
      `nc` at all, and if so which variant (netcat-openbsd vs. netcat-traditional — flag handling
      differs, notably `-w`'s "wait after EOF" vs. "connect timeout" semantics); (2) minimal/busybox
      images' `ping`/`nc` flag support (busybox's `ping -W` is not guaranteed to mean the same
      thing); (3) Windows guests need an entirely different command (`Test-NetConnection` /
      `ping.exe` with different flag spelling) — **not implemented at all**, a probe sourced from a
      Windows guest will simply fail with `execError` reporting the mock's/real agent's own
      "command not found" (pvemock: `handleGuestAgentExec`'s unrecognized-command 400; real PVE:
      whatever the guest agent reports for a missing binary — unverified); (4) whether PVE's guest
      agent `exec` even permits running arbitrary binaries by default in every guest-agent version
      vnprox needs to support, or whether an allowlist/policy can block it.
- [ ] **`classify`'s exit-code assumptions.** `internal/probe.classify` assumes iputils-ping's exit
      0/1/2 convention (0 = reply, 1 = no reply, 2 = other error) and netcat-openbsd's exit
      0-on-connect / non-zero-otherwise convention with a best-effort `"refused"` substring sniff of
      stderr to distinguish an active refusal from a generic failure — both assumptions are
      standard for these tools' current Debian/Ubuntu packaging but unverified against the exact
      package versions PVE's own guest images/templates ship.
- [ ] **`AgentExec`/`AgentExecStatus` wire shapes against real PVE.** `internal/pve`'s
      `execStatusWire` assumes `exited`/`out-data-trunc`/`err-data-trunc` are 0|1 ints (mirroring
      this codebase's other confirmed PVE numeric-boolean quirks — `internal/pve/types.go`'s
      `networkInterfaceWire`, `internal/pvemock/pvebool.go`) and that `pid` is a plain JSON number;
      neither has been captured from a real `POST/GET .../agent/exec[-status]` response.
- [ ] **Guest-agent exec privilege.** `internal/pvemock` gates `POST/GET .../agent/exec[-status]`
      on the same `VM.Audit` privilege `GET .../agent/network-get-interfaces` uses (that route's own
      existing precedent), not modeling real PVE's separate `VM.Monitor` privilege for guest-agent
      actions — confirm which privilege(s) real PVE actually requires for `agent/exec`.

## Health-check pack 2 (T-803)

- [ ] **Per-node EVPN anycast-gateway realization.** `evpn_gw_inconsistency`
      (`internal/findings/health_evpngw.go`) infers whether an EVPN zone's anycast subnet gateway is
      realized on a given member node by checking for that address on a `Bridge` entity named after
      the VNet's own id (mirroring how guest NICs in `evpn-lab.yaml` attach to e.g. `vnet-tenant-a`
      by name directly) — this codebase's own best inference from PVE's "the gateway becomes the
      anycast address realized on every zone member node" documentation (docs/features/sdn.md §2),
      not a confirmed mirror of what real PVE actually writes to `/etc/network/interfaces` (interface
      name, exact address/prefix, whether it's carried on a distinct SVI rather than the VNet bridge
      itself) on each node's EVPN VTEP. Confirm against a live PVE 8.x/9.x EVPN zone with an anycast
      gateway configured, including the timing (is it present immediately post-apply, or only once
      FRR converges?).
- [ ] **Exact `corosync-cfgtool -s` output format/version.** `internal/host.ParseCorosyncStatus`
      (backing `corosync_link_degraded`) parses one commonly-documented shape — a
      "Printing ring status." header followed by "RING ID n" / "id\t=" / "status\t=" blocks, with a
      ring classified faulty unless its status text contains "no faults" (case-insensitive). This is
      unverified against a real cluster: corosync's knet transport (the PVE default since 6.x) may
      report link status as "LINK ID"/"addr"/per-node "link enabled"/"link connected" fields instead
      of the classic ring/udpu shape modeled here, and the exact FAULTY wording is known to vary
      across corosync versions. Confirm the real output shape (and whether `-s` alone is sufficient,
      or whether `corosync-cfgtool -n` or the `corosync-quorumtool` output is needed for a fuller
      picture) against a live multi-node PVE cluster with a deliberately degraded ring, and widen
      `ParseCorosyncStatus`/`RingSpec`'s fixture shape accordingly if it turns out to differ.

## Verify live UX + eligibility check (T-806)

- [ ] **`POST /nodes/{node}/qemu/{vmid}/agent/ping` real response shape and failure mode.**
      `internal/pve.Client.AgentPing` (backing `GET /simulate/verify/eligibility`'s
      `agent-unreachable` gating) assumes this route mirrors `AgentExec`'s own confirmed
      contract exactly — a 200 with an empty/ignored body on success, and the same failure
      mode (a PVE-server-mapped error) as every other `agent/*` route when the guest agent
      isn't installed/running/reachable — by analogy with `AgentExec`/`GetGuestAgentInterfaces`,
      not from a captured real request. `internal/pvemock`'s `handleGuestAgentPing` mirrors
      `handleGuestAgentExec`'s exact `AgentUnreachable` guard for the same reason. Unverified:
      the exact response body shape, status code, and whether `agent/ping`'s failure mode is
      genuinely identical to `agent/exec`'s (real PVE's guest-agent QMP proxy could plausibly
      differ command-to-command) against a real PVE cluster and real QEMU guest agent.

## Interface renaming (issue #2)

- [ ] **Physical NIC (udev) rename + reboot realization.** The change engine renames only
      *logical* interfaces (bridge/bond/vlan) — an in-place rewrite of
      `/etc/network/interfaces` (stanza header + auto/allow-* + bridge-ports/ovs_ports/
      bond-slaves/ovs_bonds/ovs_bridge/vlan-raw-device references), applied via the normal
      ifreload path. Renaming a *physical* NIC is a udev `.link`/rule change realized only at
      the next boot, deliberately left out of the op vocabulary (the codebase's existing stance,
      InterfaceEditor's inline help). The rename dialog's "temporary until reboot / red asterisk"
      copy states this, but the exact ifupdown2 behavior when a *logical* rename targets an
      interface that is currently UP (does ifreload rename it live, or is a reboot needed there
      too?) is unconfirmed against a live PVE cluster — validate before promising "live on apply"
      for the in-use-bridge case.
- [ ] **Guest re-binding across the cluster on rename.** The engine blocks renaming an interface
      with running guests attached (safety.rename_guests_attached) and offers same-changeset
      reattach; it does not yet *auto-generate* the guest.nic.update ops. Whether PVE accepts a
      guest NIC pointing at the new bridge name mid-changeset (before ifreload realizes it) needs
      a live check.
- [ ] **VLAN child cascade.** Renaming a parent (e.g. vmbr0 → vmbrX) rewrites children's
      `vlan-raw-device` but intentionally does not rename the children themselves (vmbr0.100 stays
      vmbr0.100 on raw-device vmbrX). Confirm ifupdown2/PVE is happy with that name/raw-device
      mismatch on a real node.

## Topology v2 renderer frame budget (T-901/T-902)

- [ ] **v2 canvas renderer p95 ≤ 20ms at scale.** `docs/features/topology.md` §4's 30fps /
      ≤20ms pan-zoom target is a hardware target. Re-measured uncontended at Phase 9 close, the
      v2 canvas renderer records **p95 ≈ 50ms on the CI/dev host** (headless Chromium, software
      rasterization, no GPU) — identical to the v1 React Flow renderer measured the same way, so
      v2 is not a regression, but the 20ms budget is unverifiable in this GPU-less environment.
      `web/e2e/scale.spec.ts`'s v2 case therefore report-and-guards (headless ceiling 90ms) rather
      than asserting ≤20ms. Confirm the real p95 on a GPU-compositing browser (a normal desktop
      Chrome/Firefox, hardware acceleration on) against the `scale-lab` fixture (8 nodes × 6 NICs,
      300 guests, 40 VNets) to validate the ≤20ms hardware target, and re-tighten the assertion if
      a representative CI runner with GPU ever becomes available.

## Flow ingestion engine (T-1002)

- [ ] **IPFIX variable-length Information Elements.** RFC 7011 §7's `0xFFFF` template-field-length
      sentinel (a length-prefixed value inline in the data record) is not decoded — a template
      field declaring it is recorded with length 0, so any data set using that template is
      silently undecodable (dropped, counted, never a panic). None of this task's hand-built
      fixtures exercise it. Confirm whether real exporters vnprox is likely to see (pmacct,
      nProbe, vendor hardware) commonly emit variable-length IEs before shipping IPFIX support as
      "done" for those exporters specifically.
- [ ] **sFlow IPv6 raw-packet-header extension header chains.** `internal/flow/sflow.go`'s IPv6
      path decodes only the fixed 40-byte header; a `Record.Proto` on IPv6 traffic using extension
      headers (hop-by-hop, routing, fragment, ...) reports the header chain's first NextHeader
      value, not necessarily the true upper-layer protocol, and ports are read from whatever bytes
      immediately follow the fixed header (wrong if an extension header is present). Confirm real
      sFlow-sampled IPv6 traffic's extension-header prevalence before treating this as
      production-accurate for IPv6-heavy networks.
- [ ] **Real sFlow/NetFlow/IPFIX exporter interop.** Every `testdata/flows/*.bin` fixture is
      hand-built directly to the published wire-format specs (sflow.org's sFlow v5 spec, Cisco's
      NetFlow v9 spec, RFC 7011), never captured from a live exporter (no lab hardware/switch/
      router available in this environment — the same "Real PVE access" gap CLAUDE.md documents,
      extended here to flow exporters generally). Validate against at least one real exporter per
      protocol (a physical switch's sFlow agent, a Cisco/Juniper NetFlow export, and pmacct/
      nProbe/softflowd for IPFIX) before considering any of the three decoders field-proven.

## Host-local flow sampling (T-1004)

- [ ] **Exact `/proc/net/nf_conntrack` table format across the target kernel range (PVE 8.2+/
      9.x, docs/architecture.md D9).** `internal/flow/hostsample/conntrack.go`'s parser is built
      against the documented/observed field layout (family, family name/number, proto name/
      number, timeout, an optional tcp-only state word, then unordered `key=value` tokens twice —
      original then reply direction — plus bare flag tokens like `[ASSURED]`/`[UNREPLIED]`), and
      exercised only against hand-built fixtures (`internal/flow/hostsample/testdata/`), never a
      real kernel's live table. Confirm field layout, key set, and `nf_conntrack_acct` (packets=/
      bytes=) availability-by-default across PVE 8.2's and 9.x's shipped kernel versions —
      specifically whether accounting is enabled by default (if not, this sampler produces valid
      but always-zero-byte/-packet Records until an operator sets
      `net.netfilter.nf_conntrack_acct=1`, which this task does not do automatically and does not
      currently surface as a warning) and whether any conntrack helper (ftp, sip, ...) or IPv6
      variant emits a line shape this parser's "first-occurrence key=value scan" mis-parses.
      Measure the real poll cost of a full-table read at realistic connection-table sizes (a busy
      node can have tens of thousands of entries) against `host_sample_interval_sec`'s default
      (10s) — confirm it doesn't itself become a CPU/IO cost outweighing the feature's value on a
      loaded node.
- [ ] **Real CAP_BPF/CAP_PERFMON availability under the hardened systemd unit on a live node.**
      `packaging/debian/postinst`'s `sync_ebpf_caps_dropin` writes a systemd drop-in unioning
      `CAP_BPF`/`CAP_PERFMON` into `vnprox.service`'s `CapabilityBoundingSet` when `[flows]
      ebpf_sampling_enabled = true`, and `internal/flow/hostsample/ebpf.go`'s kernel-feature probe
      reads them back from `/proc/self/status`'s `CapEff` bitmask — neither has been exercised
      against a real systemd instance (no systemd/root environment available here; this task's
      tests run the probe logic directly as a library call, not inside an actual hardened unit).
      Confirm on a real PVE node: (1) the drop-in is picked up after `systemctl daemon-reload` +
      restart without any other hardening directive (`NoNewPrivileges=yes`,
      `RestrictAddressFamilies=...`, `SystemCallFilter=@system-service` minus the denied groups)
      silently stripping the grant or blocking the `bpf(2)`/`perf_event_open(2)` syscalls
      themselves (`SystemCallFilter=~@resources` in particular is worth double-checking against
      `bpf(2)`'s classification); (2) the actual numeric capability bit values this probe assumes
      (`CAP_PERFMON = 38`, `CAP_BPF = 39`, Linux 5.8+) match the running kernel's
      `linux/capability.h`; (3) `/sys/kernel/btf/vmlinux` is present on PVE's shipped kernel
      builds (`CONFIG_DEBUG_INFO_BTF`) — if PVE's kernel does not ship BTF, the probe will always
      fail there regardless of capabilities, and that would be worth calling out explicitly in
      product docs rather than only in a probe error string.
- [ ] **Measured CPU/memory overhead of each sampler at the Phase 9 scale target
      (`docs/performance.md`).** Neither sampler's actual resource cost has been measured against
      real traffic — only unit-level correctness (parsing, diffing, ring insertion). Concrete
      measurement plan: on a `scale-lab`-equivalent node (8 nodes × 6 NICs, 300 guests, 40 VNets
      per `docs/performance.md`'s existing scale target) or the closest available real hardware,
      (a) enable `conntrack_sampling_enabled` alone at the default 10s interval, capture
      `vnproxd`'s RSS and CPU-seconds/poll via `/proc/<pid>/status`+`getrusage`-equivalent
      sampling (or `pidstat -p <pid> 10`) over a representative traffic run, and compare against
      the same node's baseline (samplers disabled); (b) repeat with `ebpf_sampling_enabled` once
      real per-packet attachment exists (see the dependency note below) at a few representative
      packet rates; (c) record both at increasing `host_sample_interval_sec` values (5s/10s/30s/
      60s) to characterize the poll-cost-vs-resolution tradeoff a real deployment would tune
      against. Publish the resulting numbers in `docs/performance.md` once available — this task
      only establishes the measurement plan, not the numbers themselves.
- [ ] **eBPF program verifier acceptance across the supported kernel range.** Not yet applicable:
      this task deliberately does not implement real per-bridge BPF program attachment (no
      third-party eBPF loader dependency has been added — `internal/flow/hostsample/ebpf.go`'s
      `Probe` is a real kernel-feature/capability check, but `Run` never loads or attaches a BPF
      program even when the probe passes; see that file's and this package's doc comments, and
      `planning/reports/T-1004.md`'s "deviations" section). Once a follow-up task adds a real
      eBPF program (and the loader dependency decision that requires — e.g. `cilium/ebpf` — is
      made explicitly, per CLAUDE.md's "no new major dependencies without a note"), verifier
      acceptance must be confirmed across the full PVE 8.2+/9.x kernel range before shipping: a
      BPF verifier rejection is a load-time failure, not a runtime one, so it needs to be caught
      per-kernel-version, not just once.

## Latency & loss mesh (T-1303)

- [ ] **`ping` summary-line wording/format across real PVE node builds.** `internal/latmesh.
      RealProber`/`parsePingSummary` assumes iputils-ping's documented summary shape (`"N%
      packet loss"` and `"= min/avg/max/mdev"` lines) — the same PVE-node-is-Debian assumption
      T-802's guest-exec probing makes for a *guest* OS, applied here to the *host* OS vnproxd
      itself ships on. No live PVE cluster was available to confirm the exact wording/decimal
      precision/locale (`LANG`/`LC_ALL`) iputils-ping emits on a real PVE 8.x/9.x node — a decimal
      comma instead of a decimal point under a non-C locale, or a future iputils-ping version
      reformatting the summary line, would silently degrade every reading to 100% loss
      (`parsePingSummary`'s defensive "can't confirm healthy -> treat as worth flagging" fallback,
      the same stance `internal/host.ParseCorosyncStatus` already takes for a comparable
      exact-wording caveat) rather than crashing, but that's still a wrong reading, not a safe
      no-op. Confirm the daemon always execs with `LANG=C`/`LC_ALL=C` (or equivalent) and that a
      real PVE node's `ping -c 5 -W 3 <addr>` output matches the two regexes
      (`packetLossRe`/`rttAvgRe`) byte-for-byte before treating any produced latency/loss reading
      as trustworthy in production.
- [ ] **Real corosync-ring / shared-bridge latency characteristics at cluster scale.** This task's
      fixtures are synthetic time series (`testdata/latmesh/*.json`), not observed real-cluster
      RTT/loss distributions — the default thresholds (`internal/findings.HealthThresholds`'s
      `LatRttWarnMs`/`LatLossWarnPct`, 80ms/2%) are this task's own reasoned defaults (see that
      struct's doc comment), never tuned against a real corosync ring or guest-bridge fabric's
      actual jitter under load. Confirm against a real multi-node PVE cluster (ideally under a
      representative migration/backup-storm load) whether 80ms/2% is the right line between
      "genuinely degraded" and "ordinary LAN jitter" before relying on `path_latency_degraded`/
      `path_loss` findings operationally.
- [ ] **Migration/storage network fabric discovery is not implemented (scoping deviation, not a
      bug to fix blindly).** `internal/latmesh`'s Discoverer identifies exactly two fabrics —
      corosync (from `internal/host.CorosyncConfig`) and guest (shared bridge names,
      `internal/xnode.BridgesByName`) — because neither a PVE migration network
      (`datacenter.cfg`'s `migration: network=...`) nor a distinct storage network is modeled
      anywhere in `internal/inventory`/`internal/pve` today; see `internal/latmesh`'s package doc
      comment. Before implementing readers for either, confirm the exact `datacenter.cfg` key
      names/formats and how a storage network is conventionally declared (PVE has no single
      dedicated "storage network" config key the way it does for migration — it's usually implied
      by which bridge a storage-class VLAN/bond is attached to) against a real cluster, since
      guessing the shape risks the same "two names, one problem" duplication T-801 exists to
      prevent.

## Path MTU prober (T-1306)

- [ ] **`ping -M do -s <size>` DF-probe behavior across real PVE node builds.** `internal/mtuprobe.
      RealProber`/`dfProbe` assumes iputils-ping's `-M do` (Don't Fragment) flag is available and
      that a dropped DF-probe surfaces as either a `"Frag needed"`/`"Message too long"` line or a
      non-zero exit with `"100% packet loss"` — the same PVE-node-is-Debian/iputils-ping-format
      assumption `internal/latmesh.RealProber` already carries (and needs its own hardware
      validation entry above), extended here to a flag/output shape this task had no live cluster
      to confirm. Confirm on a real PVE 8.x/9.x node that `ping -M do -c 1 -s <n> -W <t> <addr>`
      behaves as `dfProbe` expects for both a size that fits and one that doesn't, across at least
      one path with a real, non-default MTU (a VXLAN/EVPN underlay is the most useful case, since
      that's exactly what `vxlan_underlay_mtu`'s measured-MTU upgrade consumes).
- [ ] **Binary-search convergence against a real, non-synthetic path.** `TestBinarySearchMTU_*`
      (`internal/mtuprobe/binarysearch_test.go`) exercises the search algorithm itself against
      scripted mock responses, not a real network path with real DF-drop latency/occasional packet
      loss unrelated to fragmentation (a lossy-but-not-MTU-limited path could in principle cause a
      false "too big" read on an unlucky probe). Confirm on real hardware whether a single-probe-
      per-size binary search is robust enough in practice, or whether production should retry a
      failed probe once before concluding "too big" (a documented, not-yet-implemented follow-up if
      real-world flakiness shows up).
- [ ] **Bond/VXLAN-EVPN path coverage scoping deviation (not a bug to fix blindly).** This task's
      card asked for probing "along each bridge/bond/VXLAN-EVPN path" but `internal/mtuprobe`
      reuses `internal/latmesh`'s existing Discoverer verbatim (corosync + shared-bridge-name
      "guest" fabric pairs only — see that package's own needs-hardware-validation entry above for
      why) rather than inventing a third, bond-specific pair-discovery mechanism this codebase has
      no substrate for (see `internal/mtuprobe`'s package doc comment for the full reasoning: a
      bond is node-local link aggregation with no node-to-node IP path of its own to path-MTU
      discover in isolation). Confirm against a real cluster whether this is a genuine, actionable
      gap (e.g. an operator wants a *specific* bond slave's own MTU verified, not just the bridge
      path riding over it) before building a third discovery mechanism.

## T-1301 — distributed packet capture engine

- [ ] **Real on-hardware capture backend (AF_PACKET/libpcap/`tcpdump`).** `internal/capture` is
      fully wired — capability gate, server-enforced un-overridable caps, BPF-filter validation,
      peer fan-out, audit, and retention sweep are all real and agent-agnostic — but the actual
      packet source is `internal/capturemock`'s scripted agent (`cmd/vnproxd/capture.go`'s
      `setupCapture` wires `capturemock.NewAgent()`), since there is no live Proxmox host to
      capture from and CLAUDE.md's stdlib-first rule bars adding a libpcap/eBPF binding here. The
      production agent (a real `tcpdump -i <iface> -w <file>` subprocess with a fixed argv, or an
      AF_PACKET reader) drops in at exactly that one wiring line — every surrounding safety
      property is already exercised by tests. Needs: confirmation that `CAP_NET_RAW` (already in
      the shipped unit's `CapabilityBoundingSet`) suffices for the chosen backend on a real PVE
      9.x node, and that the on-disk `.pcap` the real backend writes is byte-compatible with
      T-1302's decoder (the mock's classic-pcap output already is).
- [ ] **libpcap-level BPF filter compilation.** `internal/capture.ValidateFilter` is a
      conservative *syntactic* gate (shell-unsafe characters, instruction-count ceiling, known
      keywords/operators/IPs/CIDRs only) — a stdlib-only proxy for a real `pcap_compile`. The
      on-hardware agent should additionally compile the filter with libpcap before use and reject
      a compile failure; confirm on real hardware that every filter the syntactic gate accepts
      also compiles (and that the instruction-count ceiling maps sensibly to real compiled-program
      size).
- [ ] **Guest-NIC / SDN-VNet target → capture-interface resolution.** `capture.RefResolver`
      resolves bridge/bond/VLAN refs to their device name directly, but a guest NIC's live tap
      device and an SDN vnet's realized Linux device are runtime facts not derivable from the Ref
      alone — the default resolver returns `ErrUnresolvableTarget` for those (a conservative,
      safe rejection). A graph-backed resolver that maps a guest NIC to its live tap/veth on a
      real node is a follow-up (T-1302/T-1307 consume this) and needs a real cluster to validate
      the exact per-guest device naming.
## Guest network interior inspector (T-1304)

- [ ] **Exact in-guest/in-container command set per guest OS family.** `internal/guestinterior`
      (both the qemu path, `qemu.go`, and the parsers `lxc.go` shares with it) deliberately
      implements exactly one target profile — a Linux guest/container with iproute2's `ip -j addr
      show`/`ip -j route show` (JSON output support, iproute2 ~4.x+), a POSIX-ish `/etc/resolv.conf`,
      and `ss` supporting `-H -tuln` — rather than guessing a "portable" command across every guest
      OS/toolchain, mirroring `internal/probe/command.go`'s own precedent (T-802's entry above).
      Unverified against real guest images: (1) whether `ip -j` JSON output is actually present and
      stably shaped across the iproute2 versions PVE's own common guest templates (Debian/Ubuntu
      cloud images) ship; (2) minimal/busybox/Alpine images' `ip`/`ss` flag support (busybox `ip`
      has no `-j`; Alpine's `ss` comes from `iproute2-minimal` and may lack `-H`); (3) Windows
      guests and non-Linux containers need an entirely different command set — **not implemented at
      all**, a qemu guest-agent read against one will fail the same "unrecognized command" way an
      unsupported `internal/probe` target does; (4) whether `POST/GET .../agent/exec[-status]`'s
      real guest-agent exec privilege/allowlist policy (see T-802's own entry above) permits these
      three additional read-only commands the same way it permits `ping`/`nc`.
- [ ] **LXC pid-resolution mechanism** (`internal/host/containerinterior_linux.go`'s `containerPID`).
      Assumes PVE 8.x's default cgroupv2-unified layout places a running container's processes
      under `/sys/fs/cgroup/lxc/<vmid>/cgroup.procs` — this codebase's own best inference from
      pve-container's conventions, not verified against a live cluster. No cgroupv1 fallback is
      attempted (this codebase has no fixture or hardware to verify one against). Confirm against a
      real PVE node: (1) the exact cgroup path on both a fresh PVE 8.x install and an
      upgraded-from-PVE-7 node (which may still run cgroupv1 hybrid mode); (2) whether the first pid
      listed in `cgroup.procs` is always a suitable target for `nsenter --net=` (vs. a transient
      short-lived process that has already exited by the time `nsenter` runs — a race this
      implementation does not currently guard against with a retry).
- [ ] **`nsenter`/`ip`/`ss`/`ping` availability and required capabilities on the vnproxd host.**
      `Real.ContainerInterior`/`ContainerPing` shell out to these binaries via `os/exec` assuming
      they're on `PATH` and that vnproxd's own process has `CAP_SYS_ADMIN` (or runs as root) to
      enter another process's network namespace — neither is guaranteed by this task's own
      development/CI sandbox (no `/sys/fs/cgroup/lxc` exists there at all, so
      `TestReal_ContainerInterior_LiveLXC`, `internal/host/containerinterior_linux_test.go`, skips
      cleanly rather than asserting anything). Confirm vnproxd's packaged systemd unit
      (`packaging/systemd/`) grants the needed capability/runs with sufficient privilege on a real
      PVE node.
- [ ] **`defaultGatewayReachable`'s ping semantics for the lxc path.** Unlike the qemu path (which
      reuses `internal/probe.Run`'s full `Outcome` classification), the lxc path's `ContainerPing`
      collapses "no reply" and "could not attempt the ping at all" into a single `false` — a
      deliberate scope simplification (see docs/api.md's Guest interior section) this task's report
      flags rather than a verified real-hardware behavior; confirm this reads honestly enough in
      practice, or whether a follow-up should give it the same three-way `Outcome` the qemu path has.
## Conntrack & NAT table explorer (T-1305)

- [ ] **`/proc/net/nf_conntrack` field layout for state/timeout/NAT across the target kernel
      range (PVE 8.2+/9.x), independent of T-1004's own conntrack-format validation entry
      above.** `internal/host/conntrack.go`'s parser reads a superset of what T-1004's
      diff-only sampler needs — the tcp-only state word, the numeric timeout field, and (new
      here) both the original *and* reply direction tuples, diffed against each other to detect
      SNAT/DNAT (see that file's `parseConntrackLine` doc comment for the exact detection
      logic). Built and table-tested only against hand-built golden fixtures
      (`internal/host/testdata/conntrack_golden.txt`) matching the documented/observed wire
      format — never a real kernel's live table, and never a real NAT'd connection's actual
      tuple pair. Confirm: (1) the state word's exact position/vocabulary for every protocol
      family (this parser only special-cases tcp's state word; SCTP also has textual states in
      real conntrack output and is currently treated the same as UDP/ICMP — no state parsed,
      falling back to the `[ASSURED]`/`[UNREPLIED]` bracket flag if present); (2) that a real
      masquerade (SNAT) and a real DNAT/port-forward rule's conntrack entries actually produce
      the original/reply tuple divergence this parser's detection logic assumes (derived from
      documented netfilter conntrack semantics, not observed against a live NAT setup); (3)
      whether any IPv6 NAT66/NPTv6 variant, or a conntrack helper (ftp, sip, ...) with its own
      expectation entries, produces a line shape this parser mis-reads.
- [ ] **Non-root read permission on a real PVE node.** `docs/security.md` documents vnprox
      running as root with a scoped `CapabilityBoundingSet` in production, so
      `/proc/net/nf_conntrack`'s real-world `0440 root:root` permission bits (confirmed against
      *this* development sandbox, not a PVE node) should not block the read there — but this was
      never confirmed against an actual systemd-hardened `vnprox.service` unit (this task's own
      dev/e2e harness runs vnproxd unprivileged, where the read legitimately fails with EPERM;
      `web/e2e/conntrack.spec.ts`'s own header comment documents this and asserts the resulting
      `partial`/`failedNodes` degradation instead of fixture data). Confirm on a real node that
      the six capabilities `docs/security.md`'s Host footprint section lists
      (`CAP_NET_ADMIN`/`CAP_NET_RAW`/`CAP_NET_BIND_SERVICE`/`CAP_DAC_OVERRIDE`/
      `CAP_DAC_READ_SEARCH`/`CAP_CHOWN`/`CAP_FOWNER`) plus running as root are sufficient — root
      bypasses standard DAC permission checks regardless of capability set, so this is expected
      to already work, but has not been observed against a live node's actual file mode/SELinux-
      or AppArmor-equivalent MAC policy (PVE ships neither by default, but worth a one-line
      confirmation rather than an assumption).
## Migration network planner (T-1507)

Flagged from day one per this arc's "advisory, mock-first" constraint — no acceptance criterion for
this task required real PVE migration behavior, and the planner never triggers or blocks a
migration, but its two proxy assumptions below are unverified against a real cluster:

- [ ] **Whether migration traffic actually rides the shared guest-fabric bridge this package
      assumes.** `internal/migration.resolveLinkCapacityMbps` proxies "the migration network"'s
      physical capacity with the highest-capacity bridge the source/target node carry in common
      (`internal/xnode.BridgesByName`) — a reasoned inference from PVE's documented behavior
      ("absent a configured `migration: network=...`, migration traffic uses the node's default
      route"), not a confirmed observation. No live reader of `datacenter.cfg`'s `migration:
      network=...` exists anywhere in this codebase (the same gap `internal/latmesh`'s own
      needs-hardware-validation entry above already names for T-1303's identical fabric-discovery
      scope); once a real reader lands, confirm on a live cluster whether a configured migration
      network ever diverges from the guest-fabric bridge this proxy assumes, and how large the
      resulting headroom-estimate error is in practice.
- [ ] **Dirty-page-rate heuristic accuracy.** `Planner.Config.DirtyRateFraction` (default 1% of
      guest RAM/sec) is a reasoned, conservative constant, not a measurement — this arc has no live
      guest instrumentation (no dirty-bitmap read, no QMP `query-migrate` telemetry) to derive one
      from. `Assessment.BestEffort` is unconditionally `true` for exactly this reason. Confirm
      against real guest workloads (idle, moderately busy, and write-heavy database/cache
      profiles) how far this constant is from observed dirty rates before treating a `"tight"`/
      `"insufficient"` verdict's dirty-rate-driven caveat as more than a rough guide.

## Kubernetes overlay mapping engine (T-1501)

- [ ] **Real CNI variance beyond the three named defaults.** `internal/k8s.DetectCNI` is verified
      against fixture markers only (Flannel's `flannel.alpha.coreos.com/backend-type` node
      annotation, Calico's `calico-node` kube-system DaemonSet, Cilium's `cilium` kube-system
      DaemonSet) — a real cluster running a non-default install (custom Helm release names,
      Cilium in "kube-proxy replacement" mode with a differently-named DaemonSet, Calico installed
      via the Tigera operator rather than the classic manifest, or any fourth CNI such as
      Weave/Antrea/OVN-Kubernetes) has not been exercised against a live cluster and may report
      `unknown` where a human would recognize the CNI — the documented, intentional "never guess"
      behavior, but its real-world hit rate against non-default installs is unverified.
- [ ] **Node pod-CIDR advertisement across CNI/IPAM modes.** `Overlay.PodCIDRs` reads
      `Node.spec.podCIDR`/`podCIDRs` — real for Flannel and Calico's default per-node IPAM, and for
      Cilium's default cluster-scope IPAM, but unverified against Cilium configured for per-node
      IPAM extensions (ENI/Azure IPAM modes) or any CNI that manages pod addressing without ever
      populating `NodeSpec.PodCIDR` at all; such nodes simply carry no `PodCIDR` entry today
      (documented gap, `internal/k8s/overlay.go`'s doc comment), not a hardware-validation crash,
      but real coverage is unmeasured.
- [ ] **kubeconfig credential-form coverage.** `internal/k8s.ResolveContext` supports exactly two
      credential forms (bearer `token`, or `client-certificate-data`+`client-key-data`) read from
      the kubeconfig's inlined base64 `*-data` fields — real-world kubeconfigs generated by managed
      k8s providers (EKS `aws eks get-token` exec plugins, GKE's `gke-gcloud-auth-plugin`, OIDC
      `exec`-credential plugins) are explicitly unsupported and rejected with `ErrNoCredential`
      rather than guessed at; unverified whether operators actually hit this in practice (a
      long-lived service-account token kubeconfig, the form this task targets, is the common
      case for a dedicated read-only integration, but real deployment surveying would confirm).
- [ ] **Firewall-rule visibility precision for `k8s_nodeport_exposed_without_fw_rule`.**
      `internal/k8s.rulesetCovers` checks for an explicit, enabled, inbound `ACCEPT` rule matching
      proto+port on the guest's own ruleset or the cluster-scope ruleset — it does not expand
      macros, aliases, ipsets, or security groups (a documented scope limitation, `nodeport.go`'s
      doc comment), and does not evaluate PVE's default-policy fallback. A real cluster whose
      NodePort coverage comes entirely through a macro (e.g. a `Kubernetes` macro alias, if an
      operator defined one) or a security-group reference would show as uncovered here even
      though real `pve-firewall` would allow the traffic — unverified how often this pattern
      appears in practice; internal/sim's own richer match engine (`internal/sim/match.go`) already
      handles macro/alias/ipset expansion and would be the natural place to extend this check into
      if false positives turn out to be common.
## SR-IOV virtual function lifecycle (T-1506)

Named in the task card from day one, per the arc's standing "mock-first / needs-hardware-validation"
constraint — no acceptance criterion for this task required real SR-IOV hardware; both items below
are genuinely unverifiable against `internal/pvemock`.

- [ ] **Real VF creation and kernel/driver behavior.** `internal/host.VFProvisionCommands`
      (`internal/host/vfmarker.go`) renders a `vf.provision` op into `echo <N> >
      /sys/class/net/<pf>/device/sriov_numvfs` followed by per-VF `ip link set <pf> vf <id> ...`
      lines, applied via the ordinary node-file post-up/post-down path
      (`internal/change/ifaces/vfop.go`) — this task only proves those commands are *rendered*
      correctly (golden ops + apply/rollback against the fixture `host.Reader`,
      `internal/change/apply_vf_test.go`); it has no way to execute them against a real NIC.
      Real hardware/driver behavior this needs to confirm: (1) rewriting `sriov_numvfs` while VFs
      already exist and one is attached to a running guest — real Linux SR-IOV drivers commonly
      require `sriov_numvfs` to be reset to `0` before it can be increased again, which
      `VFProvisionCommands` does not currently sequence (it always writes the target count
      directly); (2) whether a VF actively passed through to a running guest can be reconfigured
      (`ip link set ... vlan/mac/spoofchk/trust`) live from the PF's host side without first
      detaching it, or whether the command silently no-ops/errors; (3) driver-specific quirks
      (ixgbevf/i40e/mlx5 etc. are known to differ on exactly which `ip link set vf` sub-options
      they honor) that could make a rendered command a no-op on some real NICs.
- [ ] **PCI address resolution via the `virtfnN` sysfs symlink
      (`internal/host.sysfsVFPCIAddr`, `internal/host/ethtool.go`).** The real (non-fixture)
      reader resolves a VF's PCI bus address by reading
      `/sys/class/net/<pf>/device/virtfn<vfID>`'s symlink target — this package's own inference
      from the kernel's documented SR-IOV sysfs convention, exercised in this task only via a
      fixture that declares `pci_addr` directly (`internal/pvemock`'s `VFEntrySpec`). Confirm
      against real hardware that `virtfnN`'s index `N` always matches netlink's own `IFLA_VF_INFO`
      VF `id` field one-for-one (an off-by-one or reordering here would silently mis-attribute a
      VF's PCI address, which the guest<->VF correlation
      — `internal/topology.ResolveVFAssignments` — depends on to match against a guest's `hostpci`
      config).
- [ ] **Firmware-level spoof-check enforcement.** `vf_spoofcheck_mismatch`
      (`internal/topology.VFPolicyMismatch`, `internal/drift/sriov.go`,
      `internal/change/validate_referential.go`'s `checkVFProvision`) treats a VF's
      `spoofchk`/`trust` bits as reported by netlink as authoritative — it has no way to confirm
      those bits are actually *enforced* by the NIC's firmware for a given driver/firmware
      combination (some SR-IOV NICs are documented to accept the `ip link set ... spoofchk on`
      call without fully enforcing it in all traffic paths, e.g. certain VLAN-tag-strip
      configurations). Confirm on real hardware that a VF configured `spoofchk on` genuinely
      cannot forge its source MAC/VLAN before treating this finding's absence as a security
      guarantee rather than a configuration-intent check.

## Ceph network awareness (T-1503)

No live Proxmox+Ceph cluster is available in this environment (docs/development.md) — every read
in `internal/ceph` is exercised against `internal/pvemock`'s fixture-driven implementation of
`GET /cluster/ceph/config`/`GET /nodes/{node}/ceph/osd`, not real PVE/Ceph.

- [ ] **Real `GET /cluster/ceph/config` / `GET /nodes/{node}/ceph/osd` wire shapes.**
      `internal/pve/ceph.go`'s `CephConfig`/`CephOSD` types are this task's best-effort modeling of
      PVE's documented Ceph API surface (`public_network`/`cluster_network` as flat keys on the
      config route; `osd`/`up`/`in`/`device` per row on the OSD route, PVE's numeric-boolean
      convention for `up`/`in` via the existing `pveBool` codec) — never independently confirmed
      against a real PVE node with Ceph installed. A field name, nesting shape, or boolean encoding
      mismatch here would silently produce an empty `ceph.Status` (this package's "absence, not an
      error" contract means a wire-shape bug degrades to "no Ceph detected" rather than a visible
      failure) rather than a wrong-but-detected read.
- [ ] **Ceph's actual `cluster_network`-unset-defaults-to-`public_network` behavior.** Real Ceph
      allows a deployment to declare only `public_network`, in which case cluster (replication)
      traffic defaults to riding the same network — `pve.CephConfig`/`ceph.Discover` never infer
      this default on the caller's behalf (an empty `ClusterNetwork` is reported as-is, "PVE
      reported none", per that type's own doc comment) to avoid guessing dressed up as a fact; not
      confirmed whether PVE's own `GET /cluster/ceph/config` response already resolves this default
      server-side (in which case this package's stance is moot) or reports it unset (in which case
      a future task should decide whether to model the default explicitly).
- [ ] **`ceph_corosync_shared_link`'s real-world saturation risk.** The finding fires on *physical
      link sharing* (same terminal NIC/bond) between corosync's ring and Ceph's cluster network —
      it has no live utilization/bandwidth data behind it (no new dependency on `internal/metrics`
      or `internal/latmesh` was added for this task), so its detail text states the qualitative risk
      without a measured "how close to saturated" figure. Confirming how quickly a real Ceph
      rebalance actually degrades corosync heartbeat latency on a shared link (and whether T-1507's
      migration planner or a future card should wire live utilization into this specific finding) is
      unverified here.
- [ ] **`ceph_cluster_mtu_mismatch`'s practical impact.** Verified only that the check correctly
      compares OSD-hosting nodes' resolved cluster-network carrier MTU (fixture-level, table test) —
      not confirmed against a real cluster whether a jumbo/non-jumbo MTU mismatch on Ceph's cluster
      network actually degrades replication throughput as sharply as the equivalent VXLAN
      encapsulation-overhead case (`vxlan_underlay_mtu`) does, or merely risks occasional
      fragmentation PMTU discovery already handles gracefully.

## T-1203 — Cross-cluster IPAM, external subnets & bidirectional sync

- [ ] **Concrete NetBox/phpIPAM production write client.** The bidirectional-sync diff engine,
      preview/apply/confirm/audit flow, and findings are complete and tested against an HTTP test
      double (`internal/ipam/sync_test.go`), but the real `ipam.ExternalIPAMClient` implementation —
      keyed to NetBox's and phpIPAM's actual REST shapes (address-object endpoints, pagination,
      auth headers/tokens, error bodies), which differ substantially between the two systems and
      across NetBox major versions — is not implemented. `cmd/vnproxd` wires the sync engine with a
      nil client (routes report "not configured") until it lands. Exact request/response shapes,
      idempotency semantics of create/delete, and how each system reports a rejected write must be
      validated against real instances.
- [ ] **Overlap semantics for intentional cross-cluster reuse.** `cross_cluster_duplicate_subnet`
      flags any overlapping CIDR across attached clusters as a `warning`. Whether operators running
      deliberately-isolated identical L2 domains in separate clusters want this as a warning, an
      info, or a suppressible finding is a UX judgment better made against real multi-cluster
      deployments than guessed here.

## T-1204 — DNS management (PowerDNS)
_(surfaced during the T-1208 v2.0 docs-freeze audit — the DNS plugin is mock-only)_

- [ ] **Real PVE SDN DNS-plugin / PowerDNS behavior.** `GET /sdn/dns` and the `sdn.dns.*` changeset
      ops are developed against `internal/pvemock`'s PowerDNS-shaped double. The real per-record
      PowerDNS API error shapes, TTL defaults, zone notify/transfer semantics, and the exact
      `/etc/pve/sdn/dns.cfg` plugin-config wire shape must be confirmed against a real PVE node with
      a configured DNS plugin. pvemock's `400` rejection shapes are modeled where known and flagged
      where unverified — do not treat them as authoritative.

## T-1205 — Guarded switch config push (gNMI/OpenConfig)
_(surfaced during the T-1208 v2.0 docs-freeze audit — switchdrv is mock-only)_

- [ ] **Real gNMI/OpenConfig vendor behavior variance.** `internal/switchdrv`'s OpenConfig/gNMI
      driver is exercised only against `internal/switchmock`. Real vendor firmware differs in
      OpenConfig path support (interfaces/vlan/lacp), transaction/commit semantics, and error
      reporting — confirm against at least one real switch before any switch is enabled in production.
- [ ] **Real LACP negotiation against physical hardware** when a `switch.port.update` changes LACP
      settings on a port participating in a live bond — timing, and whether the bond re-converges.
- [ ] **Rollback timing/atomicity on vendor firmware.** The pre-image snapshot + re-push rollback is
      proven against the mock; real firmware's write atomicity and the "switch unreachable during
      rollback → rollback-incomplete" path need hardware confirmation.
- [ ] **MLAG / stacked-switch topologies.** Port identity, LLDP-neighbor re-check, and mgmt-path
      interlock behavior on MLAG/stacked switches is untested — the LLDP-verified-port-identity
      guard assumes a single logical neighbor per port.

## T-1207 — OIDC SSO (real IdP)
_(surfaced during the T-1208 v2.0 docs-freeze audit — OIDC is tested against a mock provider only)_

- [ ] **Real-IdP claim-shape variance.** The OIDC flow is tested against an in-process mock provider
      with configurable group claims. Real IdPs (Okta, Keycloak, Azure AD/Entra) differ in the
      `groups` claim shape (array vs. space-delimited string, group IDs vs. names, claim name), JWKS
      rotation cadence, and ID-token field population — confirm the group→role mapping against at
      least Keycloak and one hosted IdP.
- [ ] **Refresh-token edge cases.** Refresh-token rotation, revocation, and re-verification behavior
      (and the "IdP refused refresh mid-session" fallback) need validation against a real IdP's
      refresh semantics, which vary by provider and by `offline_access` scope grant.

## T-1208 — v2.0 release (federation scale + PVE 10.x)

- [ ] **PVE 10.x compatibility.** No PVE-10-specific API break is known against the surfaces vnprox
      reads/writes, and every Phase 12 feature is mock-first, but no real PVE 10.x node has been
      exercised here. Per docs/roadmap-next.md's versioning section, PVE 10.x gets a validation pass
      within one phase of its release, as each prior PVE major did in v1. Confirm auth, SDN, IPAM,
      and network-apply surfaces on a real PVE 10.x node.
- [ ] **Full-daemon multi-cluster genscale HTTP + memory run.** `docs/performance.md` §10 records a
      real *aggregator-level* pass over the 3× scale-lab federation profile (`TopologySummary`
      ~14 ms/op, `Search` ~11 ms/op on a shared QEMU host). Still needed on the dev host: a
      full `runDaemon` + TLS + auth HTTP round-trip pass (p50/p95/p99 per `/federation/*` endpoint,
      like `BenchmarkAPIAtScale`) and RSS/goroutine memory for N attached clusters, plus a larger
      (10+) cluster-count ceiling for the "designated primary aggregating many clusters" case.
- [ ] **apt upgrade v1.x → v2.0 on real hardware.** The forward-only migration (v1.x schema →
      federation migrations 0021–0024) is proven in `TestMigrate_FromEachPriorSchemaVersion`, and the
      packaging/upgrade tests run on the dev host (podman + `packaging/test/upgrade.sh`), not here —
      run the real apt upgrade against a v1.x-schema DB and confirm the single-cluster surface serves
      unchanged with zero clusters attached.

## T-1702 — plugin SDK

- [ ] **Real vendor gNMI switch-driver plugin.** T-1702 re-registers T-1205's OpenConfig/gNMI
      `internal/switchdrv.SwitchDriver` through the plugin registry and proves output parity against
      a direct call using `internal/switchmock` (golden test). The real gNMI wire transport against
      physical hardware remains a `switchdrv`/T-1205 needs-hardware item (its own `ErrTransportUnavailable`
      until then); a real *third-party vendor driver plugin* pushing a bounded VLAN/description/LACP
      change to a physical switch — and its neighbor-mismatch abort — must be confirmed on hardware.
- [ ] **Out-of-process plugin resource limits.** The `procshim` transport spawns and supervises a real
      subprocess; the fault-injection test kills it mid-call and confirms graceful degradation, all on
      the loopback stdio pipe of the test binary. A real third-party plugin process's resource behavior
      (CPU/memory ceilings, the stated residual risk of unconstrained OS-level network egress from the
      plugin's own process) must be bounded operationally (systemd sandboxing / a dedicated netns /
      cgroup limits) and validated on a real deployment — the SDK states this residual risk rather than
      engineering it away.
- [ ] **In-process Go plugin loading path.** Built-ins are registered in-process by `cmd/vnproxd` and
      proven by the conformance harness; a real externally-distributed in-process plugin build/ABI path
      (Go plugin `.so` compatibility across toolchain versions) is out of this card's scope and, if ever
      offered, needs a real cross-build/version-skew validation pass.
## T-1704 — vnproxd HA (active/standby failover)

The failover/split-brain logic is fully covered by the deterministic two-daemon harness
(`internal/ha`, injected `Clock` + injectable partition switch, no real sleeps/VIP/network).
These need real multi-instance/hardware validation beyond the injected-fault harness:

- [ ] **Real VIP/ARP failover timing.** The `[ha] mode = "vip"` path only *triggers* an operator
      command; the actual virtual-IP move + gratuitous ARP convergence time (and how it interacts
      with switch MAC-aging and any upstream router's ARP cache) is unmeasured here. Validate on a
      real two-node pair behind a real switch.
- [ ] **Real DNS TTL propagation.** The `[ha] mode = "dns"` webhook path's end-to-end client
      cutover time depends on the operator's DNS automation and record TTLs — unmeasured; validate
      against a real resolver chain.
- [ ] **Real partition behavior.** The harness models a partition as a boolean switch on the
      replication link. Real behavior (asymmetric partitions, half-open TCP, TLS handshake stalls,
      clock skew between the two hosts beyond the ±30s peer replay window) needs a real pair —
      confirm the fencing margin + self-demotion timing prevents any window in which both drive a
      commit-confirm rollback, and that a healed old-active demotes before its re-armed deadline.
- [ ] **Replication throughput / lag under load.** The `ha_replication_degraded` threshold and the
      full-changesets/snapshots-each-pass replication cost are untuned against a real busy cluster's
      changeset/audit volume — measure lag and push latency on the dev host / a real pair.
- [ ] **HA-pair apt upgrade (standby-first).** The forward-only migration adds `0031_ha.sql`; the
      standby-first-then-active upgrade sequence (docs/deployment.md) is smoke-tested only against
      the injected harness — run the real apt upgrade on a two-node pair.

## T-1707 — v3.0 release (platform freeze, HA/genscale, packaging, PVE 10.x/11.x)

The v3.0 release gate. Everything provable in this environment is done (platform-API freeze docs,
threat-model rows, encrypted-at-rest tests, the 40-cycle deterministic HA failover soak,
forward-only migration proof to schema 31, docs freeze). These items require the dev host / real
hardware and are flagged, not faked:

- [ ] **HA failover-promotion latency (wall clock) at profile scale.** `docs/performance.md` §11.3
      states a *target* of ≤ `lease_ttl + fencing_margin` (≈30 s with defaults) derived from config;
      the deterministic soak proves the safety invariants (zero double-apply / zero dropped-rollback
      across 40 cycles) on a fake clock but cannot measure real promotion time. Measure the real
      active-death → standby-drives latency on two real hosts, including the operator VIP-move/DNS
      cutover (cross-references the T-1704 VIP/DNS/partition items above).
- [ ] **Full-daemon HA-pair genscale + replication-lag run.** Run the 3× scale-lab federation
      profile (§10.1) with an active/standby pair on the primary and measure real replication lag,
      push latency, and the standby's RSS/goroutine overhead under a real churn rate against the
      `500`-row `ha_replication_degraded` threshold (extends the §10.3 full-daemon genscale gap).
- [ ] **apt upgrade v2.x → v3.0 on real hardware** (and the HA-pair standby-first variant).
      Forward-only migration v2.x schema → `0025`…`0031` is proven by
      `TestMigrate_FromEachPriorSchemaVersion` (migrates to **31**, rows survive byte-for-byte), and
      the podman packaging/upgrade tests run on the dev host (`packaging/test/upgrade.sh`), not here.
      Run the real apt upgrade against a v2.x-schema DB, confirm the daemon serves unchanged with no
      v3.0 feature configured, then the standby-first HA-pair sequence.
- [ ] **Packaging + `.deb` version stamp at the v3.0.0 tag.** `make -C packaging deb`
      (amd64+arm64), the T-606 container test matrix, and a `release.yml` dry run all run on the dev
      host; confirm `dpkg -I` / `vnproxd --version` report `3.0.0` from a `.deb` built at the real
      tag (version is git-tag-derived — `packaging/version.sh` — so no code carries "3.0.0").
- [ ] **PVE 10.x / 11.x compatibility.** Carried forward and widened from T-1208's PVE 10.x item:
      no PVE-10/11-specific API break is known against the surfaces vnprox reads/writes and every
      Phase 13–17 feature is mock-first, but no real PVE 10.x/11.x node has been exercised. Confirm
      auth/SDN/IPAM/network-apply on real PVE 10.x and 11.x nodes, tracking new SDN capabilities
      (fabrics, NAT zones) per the roadmap's "validation pass within one phase of each PVE release".
- [ ] **Live third-party MCP client integration (T-1701).** The MCP transport (Streamable-HTTP/SSE +
      stdio, `2025-06-18` protocol) is exercised here only by the in-repo mock JSON-RPC client;
      confirm a real AI assistant's MCP client negotiates and drives the read/stage tools against a
      live daemon. Not a code gap — an integration confirmation.
- [ ] **Tenant coarse-scope graph expansion (T-1703).** The VLAN/VNet → member-guests/subnets
      expander is unit-tested against a hand-built inventory snapshot; confirm it resolves correctly
      against a live PVE topology at scale (the enforcement pipeline itself is proven independently
      with explicit refs).

## T-1805 — apply-time revert ticket (unattended `fw.*`/`sdn.*` revert)

The whole credential round trip — capture from the applying session, AES-256-GCM seal, SQLite row,
unseal on the timeout/crash-recovery path, non-renewing sealed-ticket `*pve.Client`, real mutating
firewall/SDN calls — is exercised end to end against `internal/pvemock` (which authenticates the
`PVEAuthCookie`/`CSRFPreventionToken` pair against its own session table, so a wrong credential
genuinely fails). These items are what only real PVE can settle:

- [ ] **PVE ticket lifetime near the boundary.** `pve.TicketLifetime` is the documented 2h, and the
      sealed ticket's `expiresAt` (and therefore the operator-facing `unattendedRevert.coversUntil`
      report) is derived from it. Confirm on real PVE that (a) a ticket really is honoured for a
      full 2h from issue, and (b) how it behaves in its final minutes for a *mutating* call —
      whether it is accepted right up to the boundary, or rejected earlier. If real PVE is stricter
      than 2h, `coversUntil` currently over-promises by that margin.
- [ ] **A sealed ticket still authorizes a firewall/SDN write minutes-to-an-hour after issue, from
      a different HTTP connection with no session cookie jar.** The unattended revert presents the
      ticket on a brand-new client the daemon builds itself. pvemock accepts this; confirm real
      `pveproxy` does not bind a ticket to anything connection- or client-scoped.
- [ ] **The end-to-end firewall-only lockout heals on iron.** A `fw.*`-only changeset applied, the
      management path then severed, the confirm window allowed to elapse with no session alive —
      and the firewall ruleset observed back at its pre-apply content. This is T-1804 scenario 5 and
      is this card's real acceptance test; it is proven here only against pvemock.
- [ ] **The same after `vnproxd` is killed and restarted inside the window** (crash recovery
      unseals from the DB and completes the revert), and after a node hard-reset.
- [ ] **`RestoreFirewallScope`'s delete-all-then-recreate against a live `pve-firewall`.** The
      firewall scope restore replays the whole ruleset; confirm real PVE tolerates the intermediate
      empty-ruleset state (and that `pve-firewall` does not compile-and-apply a wide-open or
      fully-closed ruleset in the gap) — this is the one step of the revert that is not idempotent
      in isolation. If it does, the restore needs to be reordered or bracketed.
- [ ] **Reduced-coverage reporting matches reality.** Apply a firewall changeset with a 600s confirm
      window from a session whose ticket has < 600s left, and confirm the operator-visible
      `unattendedRevert.fullWindow: false` cut-off is where the revert actually stops working.

## T-1901 — backup, restore and disaster recovery

Everything on this card runs against real SQLite, real archives, a real `flock`, a real bound
listener and a real `runDaemon`; nothing needed a Proxmox cluster. Two items are genuinely about
iron rather than about correctness:

- [ ] **`VACUUM INTO` against a months-old store on a real node.** `internal/store.SnapshotTo` is
      verified here against a concurrently-written store, but a real node's store is larger and its
      root filesystem is shared with pmxcfs and PVE's own I/O. Measure: wall time and peak extra
      disk usage for a `VACUUM INTO` of a real store, and whether the daemon's own writes visibly
      stall during it (they should not — the vacuum holds a read transaction, not a write lock).
      If the transient double-disk-usage is material on a small root filesystem, `docs/deployment.md`'s
      sizing guidance needs a number.
- [ ] **`vnprox-backup.service`/`.timer` under the real unit sandbox.** The unit runs with
      `ProtectSystem=strict`, `ReadWritePaths=/var/lib/vnprox`, `PrivateNetwork=yes` and a
      two-capability bounding set. That composition is checked here only by `systemd-analyze verify`
      (whose sole complaint is that `/usr/bin/vnproxctl` does not exist on the dev host). Confirm on
      a PVE node that `systemctl start vnprox-backup.service` writes an archive, that `--keep`'s
      prune works inside the sandbox, and that the timer's `Persistent=true` catch-up fires after a
      node was powered off across the scheduled time.

## T-1902 — support bundle export

Every collector is exercised here against real files, a real (read-only) SQLite store, real bound
listeners and a real archive; nothing needed a Proxmox cluster. Four items are about what a *real*
node's inputs look like, and each of them is a place where a redaction allowlist could turn out to
be too narrow (a useless bundle) or too wide (a leak):

- [ ] **A real `/etc/network/interfaces` from a production PVE node, especially an SDN one.**
      `host/network.json` re-emits option values through `ifaceOptionAllowlist`. The list was
      built from `internal/host`'s parser fixtures and from PVE's own rendering, not from a survey
      of real files. What to check on iron: produce a bundle on a node with OVS, bonds, VLAN-aware
      bridges, a VXLAN/EVPN SDN zone and (ideally) a hand-edited stanza, then read
      `host/network.json` and confirm (a) you could still draw the network from it, and (b) nothing
      credential-shaped survived. Anything genuinely diagnostic that came back `[REDACTED-…]` is an
      allowlist gap; anything credential-shaped that came back in the clear is a bug to fix before
      the next release.
- [ ] **`journalctl -u vnprox` on a real node.** The log collector shells out to `journalctl`
      (`-n <log-lines>`), which exists on this dev host but has never been pointed at a real vnprox
      unit's journal. Confirm the tail is what you expect, that a multi-line Go panic survives
      readably, that the byte budget truncates at a line boundary, and that `logs/summary.json`'s
      `scrubbed` count is non-zero on a node that has actually talked to PVE (a zero there on a real
      node would mean either nothing sensitive is being logged — good — or the redactor's patterns
      do not match vnproxd's real log format, which is the thing to find out).
- [ ] **Peer reachability against real peer daemons.** `peers.json` discovers nodes from
      `/etc/pve/corosync.conf` and does a bare TLS handshake against each one's `[server] listen`
      port, reporting `ok` / `unreachable` / `untrusted` and the certificate it was shown. Tested
      here against loopback (refused connections) only. On a real cluster, confirm a healthy peer
      reports `ok` with the cluster CA as issuer, a firewalled peer reports `unreachable`, and a
      peer presenting an unrelated certificate reports `untrusted` — the T-1906 trichotomy is only
      useful if all three actually occur.
- [ ] **The `--dry-run`-to-real-run size relationship on a busy node.** A bundle is meant to be
      attachable to a forum post. The budgets (20 changesets, 200 finding events, 2000 log lines /
      1 MiB) were chosen for that, not measured against a months-old cluster. Produce one on a real
      node and record the resulting archive size in `docs/deployment.md` if it is materially more
      than a few hundred kilobytes.

## T-2406 — `vnproxctl doctor --live` (2026-08-08)

- [x] **The fail-safe path is correct on real hardware.** `vnproxctl doctor --live` with no bearer
      token on `pvecube` (3.0.4+71+gc551b11) reports all four daemon-dependent checks as **skip**,
      each naming what was missing ("no bearer token (--token or VNPROX_TOKEN), or the daemon's URL
      could not be determined"), writes the same reason to stderr, and **exits 0**. That is the
      property that matters most: a stopped or unreachable daemon must never be reported as a PVE
      failure, a bad token, or a wrong clock.
- [ ] **The happy path is mock-validated only.** Verifying that `--live` returns real `pass`
      verdicts needs a T-1104 bearer token, which is minted through the SPA's Settings screen and
      therefore needs an interactive PVE login. `internal/doctor`'s tests cover the merge, the
      capability gate, and a broken fixture per check; none of that is the same as watching the real
      daemon answer. Run on hardware with a token and record the output.

## T-2405 / T-2407 — validated on `pvecube` (2026-08-09, 3.0.4+75+g60e7eec)

- [x] **The OpenAPI document is served, and served without credentials.**
      `GET /api/v1/openapi.json` returns **200** and 340,755 bytes with no cookie and no
      Authorization header, `openapi: "3.1.0"`, `info.version` matching the running build. The same
      request pattern against `GET /api/v1/topology` — a route the document describes — returns
      **401**. Both halves of the claim are what make it meaningful; either alone is not.
- [x] **Migration 0036 applied cleanly to a real store with real data.** `schema_version` 36,
      `alert_pending` present, `alert_rules` carrying `quiet_start`/`quiet_end`/`quiet_tz`/
      `quiet_bypass_error`/`digest_window_sec`, `alert_deliveries` carrying `detail`. Service
      **active**, `NRestarts` **0**, `journalctl -p err` empty, SPA `GET /` 200, RSS 14 MB. The
      pre-upgrade database is at `/var/lib/vnprox/backups/vnprox.db.pre-3.0.4+75`.
- [ ] **Quiet hours and digest coalescing have not fired on hardware.** The node has **zero alert
      rules**, so nothing has ever been deferred or coalesced there: `alert_pending` is empty and no
      delivery has been written. Everything asserted about the *behaviour* — ten events becoming one
      delivery, an event held at 23:00 arriving at 06:00, `error` bypassing the window, both DST
      directions — comes from `internal/findings`' tests against a fake clock, not from this node.
      Configure a rule with a short digest window against a local receiver and confirm one delivery
      naming N, then a quiet window spanning a real night.
- [ ] **The 30-second flush loop has not been observed doing work.** It is running (the daemon is
      up and the actor is registered unconditionally), but with no rules it has had nothing to
      flush, so "it wakes up, finds due deferrals, and delivers them" is untested outside the unit
      suite on this host.

## T-2502 — record/replay cassettes (2026-08-10)

This card built the machinery for observing PVE rather than imagining it. **The observation itself
is the part that needs hardware, and it is the whole point of the card.**

- [ ] **Record a real cluster.** `make record PVE_URL=... PVE_VERSION=... PVE_TOKEN=... PVE_NODE=...`
      against any PVE 8.x/9.x node writes `internal/pvemock/testdata/cassettes/<version>/`. Until
      that directory exists, every cassette in this repository is recorded from `internal/pvemock`
      (`cassettes/mock-three-node-vlan/`) and proves only that the pipeline works.
- [ ] **Then read the drift report.** `go test ./internal/pvemock/ -run TestFixtureCassetteDrift -v`
      compares a fixture-driven run against the cassette set and lists every field present in one
      and absent in the other. Against mock-vs-mock it currently reports 27 divergences, all of
      which are fixture-content differences between `single-node.yaml` and `three-node-vlan.yaml`.
      Against a real cassette set, **each line is a claim this repository makes about PVE that PVE
      does not support** — that list is the deliverable, and it should be filed as bugs, not
      silenced.
- [ ] **Confirm the recorder's refusal on a real login.** A ticket-auth recording session must fail
      at `POST /access/ticket` naming `body.data.ticket`, on real PVE's actual response shape and
      not just pvemock's imitation of it. If real PVE returns the ticket under a different key, the
      guard is weaker on hardware than it is in CI, and that is worth knowing before anyone trusts
      a recorded directory.
- [ ] **Confirm response-ordering stability.** ~~`pvemock` answers several list endpoints in
      map-iteration order~~ — **the mock half of this was fixed on 2026-08-13
      (`T-2502-followup-01`): every list endpoint now sorts by a documented key.** The hardware
      question is unchanged and is now the only open half: whether *real* PVE is order-stable
      across identical requests decides whether a recorded cassette can be byte-compared on
      re-recording, or only compared by content. Note the mock being stable does not answer it —
      it just means a difference observed on hardware is now attributable to PVE rather than to us.

## T-2103 — PVE compatibility matrix (2026-08-13)

The matrix in `docs/compat-matrix.json` is **entirely mock-validated**, and every cell says so.
This section records the one thing it cannot tell you.

- [ ] **Confirm the SDN Fabrics version boundary on real hardware.** The matrix's only enforced
      version divergence is that PVE 9.0+ accepts `openfabric`/`ospf` SDN zone types and 8.2 does
      not. That boundary is modelled **from Proxmox's documentation, not captured from a running
      cluster** — this project has no PVE 8.2 or 9.0 to observe, and `pvecube` is 9.2.4. If the
      real boundary sits elsewhere (a point release, a package rather than a version, an accepted
      type that then fails at apply), the matrix is confidently wrong in a way no mock run can
      surface, because the mock is asserting the same belief the matrix is testing.
- [ ] **Confirm the per-version fixtures resemble their versions.** `testdata/clusters/compat/pve-
      8.2.yaml`, `pve-9.0.yaml` and `pve-9.2.yaml` are minimal hand-written topologies, not
      captures. Only `pve-9.2.yaml` has a real counterpart available (`pvecube`), and it has not
      been diffed against it.

## T-2901 — PWA un-broken: real-device half (2026-08-15)

T-2901 fixed the v4.0.0 CSP that blocked service-worker registration and the manifest outright
(`worker-src`/`manifest-src` were `'none'`), and `web/e2e/pwa.spec.ts` now asserts in real
Chromium that the worker activates, the manifest serves as `application/manifest+json`, and an
`/embed/*` view renders inside an iframe. What Chromium-on-the-dev-host cannot prove:

- [ ] **Install the PWA on a real phone.** iOS Safari and Android Chrome each apply their own
      installability heuristics beyond the manifest being reachable; "Add to Home Screen"
      producing a standalone-window app has never been observed on either.
- [ ] **Deliver one push end-to-end through a real push service.** Every push test so far uses
      synthetic subscriptions against an httptest endpoint. A `critical` push traversing FCM
      (Android), APNs (iOS 16.4+ web push), or Mozilla autopush (Firefox) and rendering on a
      lock screen — with the fixed generic title/body and the `/tools?pushCategory=critical`
      deep link opening the installed app — closes the last unverified claim in T-2005's
      release note.
- [ ] **Confirm the offline shell on a device.** Airplane-mode relaunch of the installed app
      should serve the cached shell with `/api/*` uncached (the sw.js invariant), which a
      desktop Chromium run approximates but a phone's actual eviction behavior decides.
