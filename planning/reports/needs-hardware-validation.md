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
