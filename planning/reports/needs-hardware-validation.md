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
- [ ] **Real netlink/LLDP/bonding readers** on a PVE node with bonds, VLAN-aware bridges, and
      lldpd running (`internal/host` integration tests skip without privileges/peers;
      `TestReal_LLDP` and bond-detail tests have never run against real hardware).
- [ ] **PVE-cert reuse + hot-reload** against a real pveproxy certificate rotation.

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
