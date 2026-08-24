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

> **`T-3705` (2026-08-23) worked this file against `vnprox-dev`, the real quorate 2-node PVE
> cluster `pve-9.2.4-cluster-vnprox-dev.txt` first proved exists.** New/updated evidence:
> `planning/reports/evidence/T-3705-pvecube-2026-08-23.txt` and
> `planning/reports/evidence/T-3705-register-burndown-2026-08-23.txt`. Closed this pass: one
> `T-1906` item (cert-chain shape — see the Peer API section below; the mixed-version-rollout item
> under the same entry was briefly marked closed and then correctly reopened in the same session —
> see that item's own note for why). Three new sections were added below for five cards (T-2602,
> T-2902, T-1201/T-1407, T-2001, T-2703) that the 17-row matrix re-score named but this file had
> never carried a dedicated entry for. `docs/audit-matrix-2026-08-23.md` §3 has the full 17-row
> disposition, including which six moved to `Live` on their own primary-source evidence (T-801,
> T-1101, T-1102, T-1803, T-2303, T-3201 — the first three's own completion reports explicitly
> disclaim ever needing hardware validation) and needed no live command here beyond checking the
> report, plus T-2410 (see below). **Not attempted this session**: a full top-to-bottom
> re-verification of every one of the ~150 other open items below — this pass targeted the 17
> matrix-named cards and whatever else the same commands touched along the way. The bulk of the
> items below (bond/LACP hardware, guest-agent probes, flow-exporter interop, custom switch
> drivers, and similar) are unchanged from before this session and still carry accurate,
> already-precise blockers from prior sessions.

> `T-2410`'s `cluster-ssh` packaging job **ran to completion and passed** this session, after 5
> failed attempts whose root cause turned out to be two concurrent agent processes both running
> `make build` against the same `web/node_modules` directory at once, plus a separate `PATH` gap
> (`go` missing from a non-interactive shell's `PATH`). With those resolved, the job passed clean,
> including the previously-unexercised debt-sweep-item-8 PVE-token-copy check. One green run, not
> the three consecutive runs AC3 asks for. See
> `planning/reports/evidence/T-2410-cluster-ssh-pass-2026-08-23.log` (full transcript) and
> `planning/reports/evidence/T-2410-cluster-ssh-attempt-2026-08-23.txt` (the failed attempts).

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
- [x] **Peer API round trips, real, sustained, bidirectional** (`T-3201`, 2026-08-18, pvecube +
      pve001, PVE 9.2.10, cluster `vnprox-dev`). 971 successful `GET /api/peer/host/links` +
      `GET /api/peer/firewall/log` round trips in one sampled hour, both directions. See
      `planning/reports/blocked-validation.md` §1.1 for the journal evidence.
- [x] **`T-1906-bug-01`'s actual failure mode, observed for real** (`T-3201`, 2026-08-18).
      Verdict: **(b) already mitigated by existing code.** pvecube's `pve-ssl.pem` really does
      carry the stale IP SAN (`192.168.100.99`, confirmed again by a fresh `openssl x509 -text`
      read) and pve001 really does dial it by IP (`192.168.1.9:8007`) continuously — but zero TLS
      verification failures occurred over hours of real traffic, because `certClusterFacts`/
      `attachCertVerifyNames` (T-1906's own fix) verify each peer against its PVE node NAME
      (`DNS:pvecube`, which the cert does carry) rather than the dial IP. See
      `planning/reports/blocked-validation.md` §1.2 for the full evidence chain.
- [ ] **Still not covered on hardware**: pmxcfs replication, distributed rollback, HA lease
      fencing — needs T-3202 (failure injection) and/or a 3+-node cluster; not this card's scope.
      See `planning/reports/blocked-validation.md` §3.

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

- [x] **`vnproxctl doctor --live` run against both real nodes of a real 2-node cluster**
      (`T-3201`, 2026-08-18). `pve_reachable`/`pve_privileges` now answer for real (not skip):
      `pve_reachable` passes on both; `pve_privileges` **fails** on both — a real, new finding
      (§2.4 of `planning/reports/blocked-validation.md`): the check requires `Sys.Modify`/
      `SDN.Allocate`/`VM.Config.Network` on the daemon's own token, but `vnprox-setup` correctly,
      deliberately provisions that token read-only-only (`docs/deployment.md:59`), so a
      documented-correct install permanently fails this check. `clock_skew`/`peer_secret` are
      **still `skip` even with a real second node** — confirmed by reading
      `cmd/vnproxd/doctorlive.go`: `Env.Peers` is never wired server-side (no peer-digest route,
      `T-2406-followup-02`) and `doctorPVEProbe.Ping` always returns a zero server time (no PVE
      server-time surface, `T-2406-followup-01`) — both are still missing *code*, not missing a
      second node, so a 2-node cluster changes nothing here. A 2-node cluster therefore still
      answers exactly 8 of 10 checks under `--live`, same count as the documented single-node
      case. `T-1904-followup-02`/`T-2406-followup-01`/`T-2406-followup-02` remain genuinely open.
      Full JSON output for both nodes in `planning/reports/blocked-validation.md` §1.3.

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
      - [x] The **actual certificate chain — CLOSED, `T-3705`, 2026-08-23.** Captured on both real
            nodes: `openssl s_client -connect <node>:8007 -showcerts` serves a **1-certificate
            chain — the leaf only, no intermediate** — on both pvecube and pve001. The leaf's
            issuer line is `pve-root-ca.pem`'s own subject line verbatim (a self-signed root), so
            it is issued *directly* by the pinned root with no intermediate anywhere in this
            cluster. `internal/peer.Trust` pins the root itself and never needs the server to send
            the issuer, so a leaf-only served chain is sufficient in practice, confirmed rather
            than inferred. Evidence: `planning/reports/evidence/T-3705-pvecube-2026-08-23.txt` §1a.
      - [x] The leaf's **SANs — confirmed on real hardware, `T-3201`, 2026-08-18.** Both nodes'
            leaf certs (`pve-ssl.pem`) DO carry a management IP SAN, but pvecube's is **stale**
            (`192.168.100.99`, not its real `192.168.1.9` — `T-1906-bug-01`, first flagged
            2026-08-05 from static inspection). With a real second node (pve001) dialling it by
            IP continuously for hours, the predicted "hostname verification fails, every peer
            becomes `peer_untrusted`" did **not** happen — zero verification failures observed.
            Cause: the client verifies against the peer's PVE **node name**
            (`certClusterFacts`/`attachCertVerifyNames`, T-1906's own fix), not the raw dial
            address, and pvecube's cert *does* carry `DNS:pvecube` correctly — so the stale IP
            SAN is never consulted at all. This is the single most likely way pinning breaks on
            iron, confirmed **not** to break it, by design. Full evidence:
            `planning/reports/blocked-validation.md` §1.2.
      - [ ] Behaviour with a **custom certificate** installed
            (`pveproxy-ssl.pem`, e.g. a Let's Encrypt / enterprise-CA cert):
            such a node's peer certificate is *not* issued by
            `pve-root-ca.pem`, so pinning will reject it. Confirm and
            document the intended posture (`[peer] ca_file` pointed at the
            operator's own CA bundle is the designed answer). **Blocked:
            neither real node has a custom cert installed, and installing
            one is a live PVE config change — is destructive, needs the
            T-3704 lab.**
      - [ ] **Rotation on iron**: `pvecm updatecerts -f`, then confirm peers
            recover within one reload interval with no daemon restart, and
            that the WARN/INFO log transitions look right. **Blocked: this
            is a mutating `pvecm` call against the live cluster — is
            destructive, needs the T-3704 lab.**
      - [ ] `/etc/pve/pve-root-ca.pem` **availability** during a
            `pve-cluster` restart: how long it is absent, and that the
            last-known-good behaviour (keep verifying against the previously
            loaded anchor, WARN) is what actually happens rather than a
            fail-closed blip. **Blocked: restarting `pve-cluster` on either
            live node is destructive, needs the T-3704 lab.**
      - [ ] **Mixed-version rollout — re-examined `T-3705`, 2026-08-23, and reopened: an earlier
            pass in this same session closed this on a mistaken inference, corrected here rather
            than left wrong.** The mistake: pve001 running an older build
            (`4.0.0+39+g0f970685+dirty`) than pvecube was read as "a peer still on a pre-T-1906
            build." Checked directly instead of assumed:
            ```
            $ git merge-base --is-ancestor f5ec68e4 0f970685 && echo "YES ancestor"
            YES ancestor
            ```
            `f5ec68e4` ("Merge T-1906: pin the cluster CA for peer-API TLS") **is** an ancestor of
            `0f970685` — pve001's build already carries the pinning fix, despite being 39 commits
            behind pvecube's 101. This cluster's version skew never actually straddled T-1906, so
            the scenario this item asks about — a genuinely pre-T-1906 peer talking to a pinned
            one — remains unobserved here. (What *did* get confirmed twice, and stays closed: the
            pinned side unaffected by the *other* peer running an older build generally — see
            T-3702/T-3703's own mixed-version evidence — which is a related but distinct claim
            from this specific one.)
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
- [x] **T-3702's fix (peer response body read after its request context was
      cancelled) — CLOSED 2026-08-23 by deployment. AC2 and AC3 both verified
      on pvecube against real peer pve001; evidence in
      `planning/reports/evidence/phase-37-wave-1-deploy-verification.txt`.
      After deploying 4.0.0+101+gca079691: `context canceled` peer polls went
      8540/24h -> 0, pve001's `last_success` went from never-set (3047
      consecutive failures) to current with fails=0, and the daemon logged zero
      warnings of any kind in the post-deploy window. Original entry retained
      below for the reasoning.**
      `internal/peer/client.go`'s `do()` used to open a per-request
      `context.WithTimeout` and `defer cancel()` it before the caller ever
      read the response body, so `decodeInto`'s read raced an
      already-cancelled context; `http.Client.Timeout` (already set at
      client construction) covers connect+redirects+body-read and needed no
      help, so the fix deletes the `reqCtx`/`cancel` pair and builds the
      request on the caller's own `ctx`
      (`planning/reports/audit-2026-08-21-peer-body-cancel.md`). A new
      regression test (`internal/peer/client_bodycancel_test.go`,
      `TestClient_DoesNotCancelResponseBodyBeforeItIsRead`) reproduces this
      deterministically via an explicit stream/flush/sleep synchronisation
      point rather than relying on body size, and was confirmed to fail with
      `context canceled` against the pre-fix code and pass after — this
      closes the card's AC1.

      AC2/AC3 need the fix running on a real cluster node polling a real
      peer, which this session could not produce (CLAUDE.md: "Never apply
      network changes outside the change engine" +this card's own "Do not
      deploy anything" instruction) — the fix sits unstaged in the working
      tree, not deployed. (Committed as `ceaa32df` shortly afterwards;
      still not deployed.) **Pre-fix baseline captured for comparison, live
      on pvecube, 2026-08-23** (so the post-fix re-check has a same-day
      apples-to-apples reading): `GET /api/v1/health` shows
      `{"name":"host","node":"pve001","last_success":"0001-01-01T00:00:00Z",
      "last_error":"host links (pve001): context canceled",
      "consecutive_failures":2369}` — `pve001` has *never once* recorded a
      success. The journal shows 17,201 occurrences of `collect: peer host
      poll failed, keeping last-known state` / `context canceled` in the
      preceding 24h, matching the audit report's count almost exactly.
      **Once this fix (or T-3702 generally) is deployed to pvecube**,
      re-run both checks against the same node/peer pair
      (`pve001`, `192.168.1.7:8007`) and tick this item: `last_success`
      should go non-zero and `consecutive_failures` should reset, and no
      new `context canceled` peer-poll WARNs should appear in the journal
      after the deploy timestamp. Note the health endpoint's own naming is
      confusing here — despite the audit report's phrasing ("needs a second
      node, that is T-3704"), a second node (`pve001`) is already federated
      with pvecube in this deployment; what's actually missing is simply
      *this fix being deployed*, not a second node's existence.

## Canary apply and peer write-path parity (T-3705, 2026-08-23)

Two cards from the 17-row matrix re-score with no prior dedicated section here. Both genuinely
need a live network-config write against the real cluster that this read-only session would not
make without authorization. Evidence: `planning/reports/evidence/T-3705-pvecube-2026-08-23.txt`.

(T-801, the changeset-time cross-node consistency validator, was also named in the 17 but turned
out to be a mis-classification, not a real gap: `planning/reports/T-801.md` itself states "No
hardware-validation items — this is pure, in-process comparison logic." It never depended on a
second node in the first place, the same way T-1101/T-1102 didn't — see `docs/audit-matrix-
2026-08-23.md` §3. As supporting-not-required evidence: its shared `internal/xnode.
BridgeDivergences`/`CrossNodeMTU` primitives are exercised continuously by the live drift engine
against real per-node inventory from both nodes, with zero false-positive cross-node findings
across 5 days of real polling — confirmed by enumerating every `finding_id` family the journal has
ever logged.)

- [ ] **T-2602 (canary/staged multi-node apply) has never run against real hardware.**
      `changeset_apply_stages` (the table a staged apply's per-stage state lives in) has zero rows
      in the live store; the four most recent real changesets are all single-node. **Blocked: is
      destructive — running a real staged apply against `vnprox-dev` is a live network-config
      change, needs the T-3704 lab (or explicit operator sign-off).**
- [ ] **T-2902 (peer host-write safety parity + audit attribution) has only ever seen read
      traffic.** Every peer route this cluster has exercised in its lifetime is a read
      (`/host/links`: 93,747 calls; `/host/neighbors`: 47,165; `/host/dhcp-leases`: 15,760) — the
      five write routes it covers (`/host/stage-interfaces`, `/host/ifreload`, `/host/restore`,
      `/host/discard-staged`, `/host/lldp/install`) have zero matches anywhere in the journal,
      because no apply has ever targeted the peer node. **Blocked: is destructive — needs a real
      apply against a peer node, needs the T-3704 lab (or explicit operator sign-off).**

## Federation family: needs a second real PVE cluster, not more nodes (T-3705, 2026-08-23)

Four cards from the 17-row matrix re-score share one root cause that doesn't fit this file's
usual four hardware-blocker categories. Confirmed by a direct, read-only `sqlite3 -readonly`
query against the live store (`planning/reports/evidence/T-3705-pvecube-2026-08-23.txt` §4):
`clusters`, `external_subnets`, `wireguard_tunnels`, `wireguard_peers`, and `pinned_spec` are all
**zero rows** — none of these features has ever been configured on this install.

- [ ] **T-1201 (federation core) / T-2001 (federation cluster editor UI).** Federation attaches
      additional, separate PVE *clusters* as registry entries. `vnprox-dev` is one corosync
      cluster with two nodes — that is not a second cluster to federate with, and no second real
      PVE cluster exists anywhere in this environment. **Blocked: none of the four stated
      categories fit precisely. The honest gap is "no second PVE cluster exists to attach,"
      distinct from "needs 3+ nodes" — building one is new infrastructure beyond this task's
      scope, not a hardware limit on `vnprox-dev` itself.**
- [ ] **T-1407 (tunnel-aware federation transport).** Needs both a second federated cluster *and*
      a live WireGuard tunnel between them; `wireguard_tunnels` is also 0 rows. Same non-fitting
      blocker as above, compounded by the missing tunnel.
- [ ] **T-1203 (cross-cluster IPAM)'s remaining gap — a concrete NetBox/phpIPAM write client —
      needs a real external IPAM instance**, unrelated to PVE node/cluster count entirely; already
      recorded under its own section above with this same conclusion.

## Config-as-code (T-2703): unconfigured, not a node-count question (T-3705, 2026-08-23)

- [ ] **T-2703 (drift-to-git reconciliation) depends on T-2701's git-backed spec sync, which has
      never been configured on this install.** `grep -c '^\[git\]' /etc/vnprox/vnprox.toml` → 0,
      and the journal has zero git/PR-related log lines across its full history. **Blocked: needs
      a real git remote wired up with credentials, an app-configuration action this task's
      read-only mandate does not cover — not a hardware or node-count limit.**

## Multi-user presence and locking (T-2805)

- [x] **Cross-node presence/lock fan-out gap — confirmed still unfilled** (`T-3201`, 2026-08-18).
      `docs/project-status.md:244`'s "locks and presence are node-local; a peer-API fan-out for
      cross-node presence is a stated, unfilled gap" still holds. Confirmed by an exhaustive
      method-surface audit rather than a live round-trip test (this is an absence of code, not a
      behavior that needs traffic to observe): `internal/peer/client.go`'s full 39-method RPC
      surface has no lock/presence route at all, and `internal/presence`'s own structural test
      (`TestChangeEngineDoesNotImportPresence`) confirms the package stays in-process. A second
      real node does not change this finding — it was already fully determinable from the code,
      and this session's contribution is simply having actually checked rather than assumed.

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
      status (`GET /nodes/{node}/sdn/zones` — T-3701 corrected the URL this item originally named,
      an invented `/cluster/sdn/zones/{zone}/status` that never existed on real PVE) report an
      error, or is routed/exit-node traffic simply dark with no observable signal at all? This
      determines whether
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
- [x] **Exact `corosync-cfgtool -s` output format/version — captured, and the predicted divergence
      is real and worse than predicted** (`T-3201`, 2026-08-18, pvecube + pve001, PVE 9.2.10,
      corosync 3.x knet transport, quorate 2/2). Real captured output:
      ```
      Local node ID 1, transport knet
      LINK ID 0 udp
      	addr	= 192.168.1.9
      	status:
      		nodeid:          1:	localhost
      		nodeid:          2:	connected
      ```
      This confirms the predicted divergence ("LINK ID"/"addr"/per-node fields, not the classic
      "RING ID"/"id\t="/"status\t=" shape) — and it is **not** merely different wording for
      FAULTY: `parseRingIDHeader` only recognizes a line starting `"ring id"`, so a real `"LINK ID
      0 udp"` header is never matched at all, `cur` stays nil for the whole input, and
      `ParseCorosyncStatus` returns **zero rings, no error** — reproduced directly against this
      real captured text via a temporary Go test (`go test ./internal/host/... -run
      TestT3201RealKnetOutput -v` → `rings=host.RingStatus(nil) err=<nil>`, test removed after).
      **Consequence: `corosync_link_degraded` is a silent permanent no-op on every real PVE
      cluster running corosync's default knet transport (every PVE cluster since 6.x, per this
      entry's own prior note) — it can never fire, healthy or faulty, because it never sees a
      single ring.** Not fixed this session (needs a second parser branch for the knet block
      shape, not a one-line change) — full writeup with the exact code location in
      `planning/reports/blocked-validation.md` §2.1.
- [x] **Fixed and confirmed (2026-08-18, same-day follow-up session).** `ParseCorosyncStatus`
      gained a second header (`"LINK ID n <transport>"`) and a nested `"nodeid: N: <state>"`
      parser; a knet link's `Faulty` is `false` iff every reported peer state is `localhost` or
      `connected` (same permissive-default philosophy as the older shape). Confirmed on real
      hardware, not just the unit test: a fresh `corosync-cfgtool -s` capture from each node,
      taken after deploying the fix, fed through the exact deployed parser code (temporary `go
      run`, removed after) returned one correctly-populated, non-faulty `RingStatus` per node —
      see `planning/reports/blocked-validation.md` §2.1 for the full command/output. **Still
      open**: the real FAULTY-state wording was not live-observed (no corosync link disruption was
      attempted against the live cluster this session either) — the permissive default is design,
      not confirmed wording; whoever next has real hardware and can safely disrupt corosync
      connectivity for well under a minute should close this specific remaining gap.
- [ ] **Corosync ring status is still local-node-only, confirmed unchanged** (`T-3201`,
      2026-08-18). `docs/features/monitoring.md` §5's scope note ("production wiring reports only
      this daemon's own local node's ring status today — cluster-wide peer fan-out needs a new
      peer API route") still holds: `internal/peer/client.go`'s full 39-method RPC surface (every
      `func (c *Client) ...` method enumerated) has no corosync route at all. A second real node
      does not change this — the gap is an absence of code, not a precondition on cluster size.

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
- [x] **Real sFlow/NetFlow v5 wire delivery over a real network — PARTIALLY CLOSED, `T-3706`,
      2026-08-24.** Still no physical switch/router/OVS available (the disposable nested lab has
      no real NICs either — an unchanged limit), so this couldn't be a genuine exporter's own
      agent. What it CAN close, and does: whether vnproxd's listeners correctly bind, receive, and
      decode real UDP datagrams delivered over a real (if nested) L2/L3 path end-to-end into
      `flow_samples`, under the exact hardened `vnprox.service` systemd unit — none of which a
      loopback/in-process unit test exercises. Hand-built (independently of this package's own
      code — from the published wire specs directly, sflow.org's sFlow v5 spec and the long-stable
      NetFlow v5 fixed format, not by reusing `testdata/flows/*.bin` or any decoder-adjacent
      helper) one sFlow v5 datagram and one NetFlow v5 datagram, sent from `pve-lab-2` to
      `pve-lab-1`'s real listeners over the lab's `vmbr0`. Both decoded correctly and landed in
      `flow_samples` with every field intact (`planning/reports/evidence` doesn't yet have a
      transcript filed — see the T-3706 completion report for the full field-by-field capture).
      **Found and fixed in the process:** the sFlow decoder (`internal/flow/sflow.go`) was
      discarding `frame_length` — every sFlow-sourced record was silently stored as
      `bytes=0, packets=0` regardless of what the exporter reported, undetected because the
      package's own `decode_test.go` golden fixture asserted a `want` Record that also never set
      Bytes/Packets, the same self-authored-fixture-agrees-with-itself failure shape as the
      SDN-zone-status defect. Fixed to `Bytes = frame_length, Packets = 1` (the literal per-sample
      reading — deliberately not extrapolated by `sampling_rate`, see the fix's own doc comment).
      NetFlow v5 needed no fix — `dOctets`/`dPkts` were already read into Bytes/Packets correctly.
      **IPFIX: listener confirmed to bind and log correctly on the lab; no live wire packet was
      sent** (template-based, more setup than the remaining time in this pass allowed) — its own
      `Bytes`/`Packets` wiring was code-reviewed instead (shares `template.go`'s field-mapping path
      with NetFlow v9, which does correctly populate both from `octetDeltaCount`/`packetDeltaCount`
      — the sFlow bug above was specific to sFlow's own non-template raw-packet-header path, not
      present in the shared template code IPFIX/NetFlow v9 use), but this is a code-review finding,
      not an observed one, and is flagged as such rather than claimed proven. **Still open:**
      genuine third-party exporter interop for all three protocols (a real switch's sFlow agent, a
      Cisco/Juniper NetFlow export, pmacct/nProbe/softflowd for IPFIX) — none of that changed here,
      and NetFlow v9's own template-refresh/expiry behavior under a real exporter's cadence is
      still untested.

## Host-local flow sampling (T-1004)

- [x] **`/proc/net/nf_conntrack` does not exist on PVE 9.2 — CLOSED (dead-negative), `T-3706`,
      2026-08-24.** Enabled `conntrack_sampling_enabled` on the disposable nested lab
      (`pve-lab-1`, PVE 9.2.0), generated real inter-node traffic (ping + curl to pve-lab-2),
      and watched `journalctl -u vnprox`: `hostsample: conntrack poll failed … open
      /proc/net/nf_conntrack: no such file or directory`, repeating every `host_sample_interval_sec`
      (10s) with zero successful polls. Checked the SAME path against `pvecube` (read-only, PVE
      9.2.14) for comparison: also absent, despite `nf_conntrack`/`nf_defrag_ipv4`/`nf_defrag_ipv6`
      all loaded (`lsmod`) and every `/proc/sys/net/netfilter/nf_conntrack_*` sysctl present — i.e.
      the conntrack subsystem is fully active, but this kernel build has
      `CONFIG_NF_CONNTRACK_PROCFS` compiled out (a modern-kernel-wide trend: netlink/the
      `conntrack` CLI, both present and working on this node, are the supported interface now).
      This is not a format/layout question the checklist item below anticipated — it is total:
      **every poll fails, on every PVE 9.2 node checked, always.** `internal/host/conntrack.go`
      (T-1305's live conntrack/NAT explorer) reads the exact same path with the exact same "present
      whenever nf_conntrack is loaded" assumption in its own doc comment and is equally affected,
      though that package is out of T-3706's scope to fix. Filing this as the closure of the item
      below rather than leaving both open: the format question is moot when the file never exists.
      A real fix needs either shelling out to `conntrack -L` (parsing its own distinct text format)
      or a netlink-based reader (`nfnetlink_conntrack`) — both bigger than a config toggle, and
      neither attempted here (scope: T-3706 is the flow stack's dev-fixture/lab-enablement card,
      not a rewrite of two packages' conntrack source). `ebpf_sampling_enabled`'s probe-and-log
      fallback path is unaffected (it never reads this file) but per its own doc comment does not
      attach a real per-packet BPF program yet either, so `conntrack_sampling_enabled` remains the
      only host-local sampler with anywhere near a working implementation, and it does not work.
- [ ] **Exact `/proc/net/nf_conntrack` table format across the target kernel range (PVE 8.2+/
      9.x, docs/architecture.md D9) — MOOT per the CLOSED item directly above; kept for the
      record.** `internal/flow/hostsample/conntrack.go`'s parser is built
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

- [x] **`ping` fails outright under `vnprox.service`'s own shipped systemd hardening — not a
      wording/format problem, `ping` never runs at all** (`T-3201`, 2026-08-18, pvecube + pve001,
      PVE 9.2.10). Confirmed for `internal/mtuprobe`'s identically-shaped `ping` subprocess call
      (see the Path MTU prober entry below for the full evidence and root cause:
      `CapabilityBoundingSet=` in `vnprox.service`'s unit lacks `CAP_SETPCAP`, so modern
      iputils-ping's own `cap_set_proc()` privilege-drop call fails and it aborts, exit 255,
      `"ping: cap_set_proc: Operation not permitted"`, before ever sending a packet).
      `internal/latmesh.RealProber`'s own `ping` invocation (`internal/latmesh/prober.go:89`) is
      the same binary, the same systemd scope, the same missing capability — very likely hit
      identically, which would mean `parsePingSummary` never sees real ping output at all and
      every latency/loss reading on a hardened install is the "can't confirm, treat as worth
      flagging" fallback this entry's own note already anticipated, just for a completely
      different reason (the daemon's own sandboxing, not a wording/locale mismatch). **Not
      independently re-verified with its own debug capture** (budget went to `mtuprobe` first) —
      see `planning/reports/blocked-validation.md` §2.6 for the full writeup and candidate fixes.
- [x] **`internal/latmesh` exposure independently confirmed (2026-08-18, same-day follow-up
      session), on real hardware, not just inferred by code shape.** A real `.deb` redeploy
      (before the full two-part fix below had landed) triggered fresh `health:path_loss`
      `"transition":"new"` notifications on pve001 for `corosync:ring0` **and all four
      `guest:vmbrN` fabrics** simultaneously — `internal/latmesh/prober.go`'s `parsePingSummary`
      was confirmed by direct code reading to fall through to `Reading{LossPct: 100}` for a hard
      `capset`/`cap_set_proc` exec failure (non-empty output means the `len(out)==0` exec-error
      branch is never reached; no `"packet loss"` line to match means `lossFound` stays `false`).
      Fixed by the same two-part fix as `internal/mtuprobe` below (`CAP_SETPCAP` +
      `SystemCallFilter=capset setuid`) — confirmed clearing via `GET /latmesh/heatmap` on the
      real daemon post-fix, see `planning/reports/blocked-validation.md` §2.6.
- [ ] **`ping` summary-line wording/format across real PVE node builds** — moot until the
      `CAP_SETPCAP` finding above is fixed (ping cannot run at all right now, so its output
      wording is unobservable in production); left open, now correctly understood as
      second-order to the finding above rather than the primary risk. `internal/latmesh.
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

- [x] **`ping -M do -s <size>` DF-probe behavior across real PVE node builds — confirmed, and the
      real failure mode is not fragmentation-related at all** (`T-3201`, 2026-08-18, pvecube +
      pve001, PVE 9.2.10). `internal/mtuprobe`'s DF-probe floor check
      (`internal/mtuprobe/prober.go`'s `dfProbe`, called via `BinarySearchMTU`) reported the real
      corosync ring0 path (`pvecube` → `192.168.1.7`, a real, working IP sourced from
      `/etc/pve/corosync.conf`) could not carry even 552 bytes, every ~5 minutes, sustained:
      ```
      {"level":"WARN","msg":"mtuprobe: path could not carry even the minimum MTU, keeping prior reading","linkId":"corosync:ring0|pvecube->pve001","minMtu":552}
      ```
      The identical command run by hand as root over SSH (`ping -M do -c3 -W2 -s524 --
      192.168.1.7`) succeeded cleanly every time (0% loss, 0.5ms avg) — so this is not a DF-probe
      output-parsing mismatch (`dfProbe`'s "Frag needed"/"Message too long"/"1 received" parsing
      is fine) and not a real MTU/PMTU problem on this network at all.

      **Root cause, confirmed directly via a temporary debug build deployed to pvecube** (binary
      swap + restart, reverted immediately after one capture — no diff left in the tree):
      ```
      TEMP-T-3201-DEBUG dfProbe target="192.168.1.7" size=552 payload=524 err=exit status 255 text="ping: cap_set_proc: Operation not permitted\n"
      ```
      Modern iputils-ping opens its raw socket, then calls `cap_set_proc()` to drop capabilities
      it no longer needs as its own defense-in-depth — and **aborts hard if that call itself
      fails**, even though the socket it needed was already open. `cap_set_proc()` needs
      `CAP_SETPCAP`, and `vnprox.service`'s shipped `CapabilityBoundingSet=` (`CAP_NET_ADMIN
      CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_DAC_OVERRIDE CAP_DAC_READ_SEARCH CAP_CHOWN
      CAP_FOWNER`) does not include it — so `ping`, execed as vnproxd's own child, can **never**
      succeed under this daemon's own shipped systemd hardening, on any node, regardless of what
      it's pinging or at what size. This is why manual reproduction (outside `vnprox.service`'s
      systemd scope entirely) did not reproduce the failure. See
      `planning/reports/blocked-validation.md` §2.6 for the full writeup, the ruled-out
      alternative causes, and candidate fixes (none applied this session — a systemd-hardening
      security tradeoff, not a one-line bug).
- [x] **Fixed and confirmed (2026-08-18, same-day follow-up session) — `CAP_SETPCAP` alone was
      NOT sufficient, proven live.** Adding just `CAP_SETPCAP` to `CapabilityBoundingSet=`
      (deployed and restarted on both real nodes) still failed identically five minutes later — a
      second debug capture found `vnprox.service`'s `SystemCallFilter` also denies `@privileged`,
      which `capset` (the syscall `cap_set_proc()` actually makes) is a member of, blocking it at
      the seccomp level regardless of capabilities held. `SystemCallFilter=capset setuid` (a
      second syscall, `setuid`, needed for iputils-ping's UID-level defense-in-depth step after
      `capset()`) added alongside the capability fix — both confirmed minimal via bisection
      (`setgid`/`setresuid`/`setresgid`/`setgroups` were all unnecessary). Confirmed on the real,
      redeployed daemon: `systemctl show` on both nodes lists `capset`/`setuid` as allowed; the
      live daemon's own `GET /latmesh/heatmap` shows a fresh real sample at `lossPct: 0` for
      `corosync:ring0`, and the `mtuprobe: path could not carry even the minimum MTU` warning did
      not recur in the observation window. Full command-by-command reproduction ladder and the
      final unit-file/docs wording in `planning/reports/blocked-validation.md` §2.6.
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
- [x] **The end-to-end firewall-only lockout heals on iron.** **Done, 2026-08-18 (T-3202 Scenario
      5).** A `fw.*`-only changeset (cluster+node `fw.options.update` enabling the ruleset, plus an
      `fw.rule.create` DROP of this operator's own management-port traffic) applied against
      `pvecube`, confirm window (30s) allowed to elapse with no session alive, unattended revert
      fired via the sealed PVE ticket and restored the firewall scope to its pre-apply content —
      confirmed live (`enable:0` at cluster/node scope, digest matching, no orphaned rule) not just
      by the changeset's own self-report. Two real bugs were found and fixed getting a clean result
      (`fw_verify`'s `GET .../firewall/status` doesn't exist on real PVE; the rollback's
      `PolicyIn`/`PolicyOut` restore rejected by real PVE when never explicitly set) — see
      `planning/reports/blocked-validation.md` §2.7 and `planning/reports/T-3202-scenarios.md`'s
      Scenario 5 for full evidence.
- [ ] **The same after `vnproxd` is killed and restarted inside the window** (crash recovery
      unseals from the DB and completes the revert), and after a node hard-reset. Deferred in
      `T-3202-scenarios.md` as Scenarios 2/3, not yet attempted.
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

- [x] **Confirm the SDN Fabrics version boundary on real hardware.** — **ANSWERED 2026-08-16, and
      the answer was "the boundary was on the wrong surface entirely."** This item read: *"If the
      real boundary sits elsewhere ... the matrix is confidently wrong in a way no mock run can
      surface, because the mock is asserting the same belief the matrix is testing."* That is
      precisely what had happened, for four phases.

      Queried against `pvecube` (PVE 9.2.4), read-only, capture at
      `planning/reports/evidence/pve-9.2.4-sdn-schema.txt`:

      - The real 9.2 SDN zone type enum is `<evpn | faucet | qinq | simple | vlan | vxlan>`.
        `openfabric` and `ospf` **are not zone types on PVE 9**. Real 8.2 and real 9.2 both reject
        an `openfabric` zone, so the modelled divergence did not exist in either direction.
      - Fabrics are a separate family: `POST /cluster/sdn/fabrics/fabric --id <string> --protocol
        <bgp | openfabric | ospf | wireguard>`. `openfabric`/`ospf` are two of four fabric
        *protocols*. The other two, `bgp` and `wireguard`, appear in no vnprox document.
      - `GET /cluster/sdn/fabrics/all` on 9.2 returns `{"fabrics":[],"nodes":[]}`.

      The gate was repointed to the divergence that does exist (presence of the
      `/cluster/sdn/fabrics` family), renamed `sdn_fabrics_api_gate`, and mutation-proved at both
      layers. See `docs/compatibility.md`'s "What is modeled" for the correction notice, and
      `T-3101` for the modelling work this unblocks.

      Two further gaps the same capture exposed, both carded in `T-3101` rather than fixed in
      passing: `faucet` is a real zone type **and** a real controller type that
      `internal/change.validSdnZoneTypes` rejects, and `/cluster/sdn` carries `prefix-lists` and
      `route-maps` families vnprox does not model at all.

- [ ] **Confirm the 8.2 half of that boundary.** The correction above is captured from 9.2.4 only.
      That PVE 8.2 does not serve `/cluster/sdn/fabrics` is inference from the feature's 9.0
      release, not observation — this project still has no 8.2 to query. It is a much safer
      inference than the one it replaced (the family demonstrably post-dates 8.2), but it is an
      inference, and this line exists so nobody mistakes it for a capture.
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

### Validated on `pvecube` (2026-08-16, `4.0.0+5+g0af968b+dirty`)

- [x] **The daemon actually serves an installable PWA on a real node.** The Phase 29 package was
      deployed to `pvecube` and `vnproxctl verify -only pwa.servable` reported **`1 passed, 0
      failed, 0 skipped`** against it — the app shell's CSP admits `worker-src 'self'` and
      `manifest-src 'self'`, `/manifest.webmanifest` returns 200 as `application/manifest+json`,
      and `/sw.js` returns 200. Before the upgrade the same node served
      `worker-src 'none'; manifest-src 'none'` and the manifest as `text/plain; charset=utf-8`
      (captured with `curl -D-` immediately before `apt install`), so this is a before/after on
      real hardware, not a fixture. This closes the machine-checkable half of status-matrix
      row 73; the three items above are the human half and remain open.

**A defect in the check itself, found by this deployment and fixed the same day.** The first run
on `pvecube` reported `0 passed, 0 failed, 1 skipped`. `pwa.servable` read its `RootProbe` off
`Deps.Daemon`, which `vnproxctl` only builds when a bearer token is configured — and a freshly
installed node has no token. **The one check written to detect the v4.0.0 CSP defect therefore
skipped on exactly the deployments that had it**, while printing a message telling the operator
to supply a `--token` that none of its three (unauthenticated) fetches needs. `Deps.Root` now
carries an anonymous prober built from the daemon URL alone. Three regression tests cover it, and
the fix was mutation-verified: reverting `rp := d.Root` to ignore the new field turns both the
pass and the fail case back into skips.

The lesson generalises past this check, and is why it is written here rather than only in a commit
message: **a check that skips is indistinguishable from a check that is not wired**, and this one
had passed its own unit fixtures — which supplied a `Deps.Daemon` — for a week. Only running it
against a real deployment in that deployment's default state exposed it.

### Two more checks that were not checking (2026-08-16, same deployment)

With `pwa.servable` running, the full hardware suite was run on `pvecube` for the first time since
Phase 29. It reported **2 failed**. Both were defects in the checks, not in the product — and both
had the same root cause as the skip above: the check's own fixture was written from the author's
memory of an interface rather than from the interface.

- [x] **`backup.archive_round_trip` called every healthy node's backup empty.** It reported
      "wrote a 0-byte archive: an empty backup of a live store" while a hand-run
      `vnproxctl backup` on the same node, at the same moment, wrote 720 KiB. The check decoded
      `sizeBytes` and `includedKeys`; `vnproxctl backup -o json` emits `bytes` and
      `includesKeyMaterial`, and has never emitted the other two. `encoding/json` leaves absent
      fields at their zero value, so the size assertion failed permanently **and the key-material
      assertion — "a backup must not include key material unless `--include-keys` was passed" —
      could never fire at all.** That second half is the one that matters: it is a security
      assertion that had been dead for its whole life. Now passes on `pvecube` with real numbers
      (742,360 bytes, schema 48). Pinned by a golden captured from the CLI itself
      (`internal/verify/testdata/vnproxctl-backup.json`) which both sides assert against, so a
      future rename fails a test instead of a deployment.
- [x] **`peer.ca_pins_real_chain` reported a false failure against a correct node.** It demanded
      the dial address appear in the leaf's SAN list — **the pre-T-2303 rule**. T-2303 changed
      peer verification specifically so a certificate covering the node name is verified against
      that name (`certs.ResolveVerifyName` rules 1 and 2), which is what makes a stale or absent
      IP SAN survivable. `pvecube`'s real PVE leaf carries `DNS:pvecube`,
      `DNS:pvecube.localdomain.` and an IP SAN for a different interface than the one it is
      reached on, so the check flagged `T-1906-bug-01`'s "failure mode" against a node where the
      fix for that bug was working exactly as designed. Its evidence was also rendered through
      `firstLine()`, and `openssl x509 -ext subjectAltName` prints the header on line 1 with every
      SAN on line 2 — so the failure displayed an **empty** SAN list for a certificate carrying
      six. In a report built to be sent to strangers, that is worse than saying nothing. The check
      now asserts what the product asserts, and the broken fixture in `checks_test.go` (which had
      encoded the same wrong expectation) was corrected to break the check the only way that
      genuinely fails closed: covering neither the node name nor the address.

**Suite state after the fixes: `5 passed, 0 failed, 13 skipped`.** Eleven of the thirteen skips
are legitimately token-gated — unlike `pwa.servable`, they read authenticated API surfaces — and
two are environmental (`pvecube` has no LACP bond; switch push is dark by config). One,
`sriov.vf_capable_nic_present`, skipped because its enumeration command errored rather than
because the hardware was absent — **and looking at it found the fourth instance of this arc's
recurring bug rather than a false alarm.**

- [x] **`sriov.vf_capable_nic_present` reported an unknown for a known negative.** The enumeration
      ends in `[ -e "$f" ] && echo ...`, which is also the `for` loop's exit status, so a host with
      no SR-IOV-capable NIC makes `sh` exit 1 and the check said *"could not enumerate SR-IOV
      capability"* instead of *"this host has no SR-IOV-capable NIC, or IOMMU is not enabled"* —
      the branch that says the useful thing was unreachable on exactly the hosts it was written
      for. This one hides a known behind an unknown, the mirror of the other three; the defect is
      the same in both directions. A trailing `exit 0` restores the distinction, and `pvecube` now
      reports the fact. Still a skip — the hardware genuinely is absent — but for a reason an
      operator can act on. `checks_destructive.go` carried the identical command and is fixed with
      it.

## T-3101 — SDN Fabrics (2026-08-17)

pvecube has no fabrics configured and is a single node, so the capture
(`planning/reports/evidence/pve-9.2.4-sdn-schema.txt`) proves the fabrics/prefix-lists/route-maps
API's *shape* only — none of the following is observed against real hardware:

- [ ] **Fabric convergence and per-node realization.** The captured API has no
      `/cluster/sdn/fabrics/fabric/{id}/status` route the way a zone has a real per-node status read
      (`GET /nodes/{node}/sdn/zones` — corrected by T-3701; this entry originally compared against an
      invented `/cluster/sdn/zones/{zone}/status` that never existed on real PVE either) —
      `internal/sdn.Fabric.NodeStatus` (built from `GET /cluster/sdn/fabrics/node`) therefore
      reports configured *membership* only, with every member node hardcoded to `status: "ok"`.
      Whether real PVE exposes fabric health any other way (a task log, a different route,
      `journalctl` on the node) is unconfirmed.
  - [ ] `pve.SDNFabricNode`'s `ip`/`ip6` fields are this package's inference from `--ip_prefix`'s
        stated purpose ("The IP prefix for Node IPs"), not a captured field name — the fixture
        cluster's fabrics were all empty, so `GET /cluster/sdn/fabrics/node` was never observed
        with real rows.
- [ ] **`sdn.fabric.update`'s protocol immutability.** `internal/change.SdnFabricUpdateParams`
      omits `Protocol` on the assumption that changing a fabric's protocol is a delete+create,
      mirroring `SdnZoneUpdateParams`' `Type` immutability — but the capture has no
      `pvesh usage /cluster/sdn/fabrics/fabric -v`'s `set`/PUT usage block at all (only
      `get`/`create`), so this is unconfirmed either way.
- [ ] **What a fabric actually does to the underlay.** The capture proves the API's shape, not FRR
      convergence, VRF wiring, or WireGuard tunnel establishment for a `protocol=wireguard`
      fabric on a real multi-node cluster.
- [ ] **`prefix-lists`/`route-maps` field shape.** `ls /cluster/sdn/prefix-lists` and
      `ls /cluster/sdn/route-maps` both returned empty (family exists, no entries) — no
      `pvesh usage` output was captured for either, so `pve.SDNPrefixList{ID}`/`pve.SDNRouteMap{ID}`
      model only a name/id field, inferred from this package's own SDN-object convention rather
      than observed. Their coupling to T-3102's controllers (`--route-map-in`/`--route-map-out`)
      is asserted in prose only — establishing it against a populated capture is T-3102's job.

File these under `T-3201` per this file's own convention (cross-node/hardware-only checks).
**`T-3201` status (2026-08-18): not attempted.** Confirming any of the above needs actually
configuring a real SDN fabric on this cluster — a mutating PVE SDN write, outside T-3201's
read-mostly validation scope. Still open, waiting on a future card scoped to SDN object
configuration.

## T-3102 — SDN Controllers (2026-08-17)

pvecube has no controllers configured and is a single node, so the capture
(`planning/reports/evidence/pve-9.2.4-sdn-schema.txt`) proves the controllers API's *shape* only —
none of the following is observed against real hardware:

- [ ] **Per-type field assignment is inferred, not captured.** Unlike the fabrics section's
      `pvesh usage` transcript (which has explicit "Conditional options:" groupings per protocol),
      the controllers section's transcript has no equivalent grouping — every field
      (`asn`/`bgpMode`/`ebgp`/`ebgpMultihop`/`peers`/`fabric`/`peerGroupName`/`routeMapIn`/
      `routeMapOut`/`isisDomain`/`isisIfaces`/`isisNet`/`node`/`nodes`/`loopback`) is listed flat,
      with only its own English description to go on. `internal/change/validate_schema.go`'s
      `sdnControllerTypeFields` and `internal/pvemock/sdn_controller.go`'s mirrored map assign bgp
      -> asn/bgpMode/bgpMultipathAsPathRelax/ebgp/ebgpMultihop/peers, evpn ->
      fabric/peerGroupName/routeMapIn/routeMapOut, isis -> isisDomain/isisIfaces/isisNet, faucet ->
      none, by reading each field's description rather than a captured grouping. Whether real PVE's
      own parameter-schema enforcement actually draws the line there (as opposed to, say, letting an
      evpn controller also carry an asn) is unconfirmed — re-run
      `pvesh create /cluster/sdn/controllers --controller test1 --type evpn --asn 65000` (or the
      isis/faucet equivalents with a field this model excludes) against pvecube and check whether it
      is accepted or rejected, then correct both maps together if it disagrees.
- [ ] **Controller convergence and per-node realization.** The captured API has no
      `/cluster/sdn/controllers/{id}/status` route the way a zone has a real per-node status read
      (`GET /nodes/{node}/sdn/zones` — corrected by T-3701; this entry originally compared against an
      invented `/cluster/sdn/zones/{zone}/status` that never existed on real PVE either), and (unlike
      fabrics) no separate `/cluster/sdn/controllers/node` per-node-membership collection either —
      `internal/sdn.Controller` therefore carries no `nodeStatus` at all. Whether real PVE exposes
      controller health or per-node membership any other way is unconfirmed.
- [ ] **`sdn.controller.update`'s type immutability.** `internal/change.SdnControllerUpdateParams`
      omits `Type` on the assumption that changing a controller's type is a delete+create, mirroring
      `SdnFabricUpdateParams`'/`SdnZoneUpdateParams`' own immutability — but the capture has no
      `pvesh usage /cluster/sdn/controllers -v`'s `set`/PUT usage block at all (only `get`/`create`),
      so this is unconfirmed either way.
- [ ] **EVPN/BGP session-health re-attachment (`internal/evpn.controllerHealth`).** The
      controller<->session matching is by peer IP address (`SdnController.peers` against observed
      `NodeStatus.Peer.PeerAddr`) — never exercised against a real controller with a real configured
      peer list and a real FRR session to it, only against pvemock/unit-test fixtures. Whether real
      PVE's controller `peers` field is formatted/populated exactly the way this matching assumes is
      unconfirmed.
- [ ] **What a controller actually does to the underlay.** The capture proves the API's shape, not
      FRR/BGP session establishment, EVPN route exchange, or IS-IS adjacency formation for a real
      controller on a real multi-node cluster.

File these under `T-3201` per this file's own convention (cross-node/hardware-only checks).
**`T-3201` status (2026-08-18): not attempted** — same reasoning as the Fabrics section above.

## Firewall fidelity: `forward` direction and vnet scope (T-3103, items 1–2)

`planning/reports/evidence/pve-9.2.4-sdn-schema.txt` directly confirms the `forward` rule
direction at cluster, node, and vnet scope, and `policy_forward` at cluster and vnet scope; a few
narrower corners were not directly captured and are modelled conservatively (honest rejection)
rather than guessed at:

- [ ] **`policy_forward` at node scope.** The capture's `--type <forward | group | in | out>` rule
      enum is independently confirmed at `/nodes/pvecube/firewall/rules`, but the capture never ran
      `pvesh usage /nodes/pvecube/firewall/options -v` — only `/cluster/firewall/options` and
      `/cluster/sdn/vnets/labnet/firewall/options` were captured. `internal/change.
      schemaFwOptionsForScope` allows `defaultForward` at node scope by inference (node/cluster
      already share one `FirewallOptions` wire shape for `policy_in`/`policy_out`, and the rule-type
      enum is confirmed symmetric across all three scopes), not from a direct capture. Confirm
      `pvesh usage /nodes/pvecube/firewall/options -v` shows `--policy_forward` before trusting a
      node-scope `fw.options.update` with `defaultForward` set against real hardware.
- [ ] **`log_level_forward` at cluster and node scope.** The capture's `"### usage /cluster/
      firewall/options | forward"` excerpt shows `--policy_forward` and (as apparent alphabetical
      context) `--policy_in`, but never independently matches a `--log_level_forward` line the way
      the vnet-scope options section does — despite `log_level_forward` itself containing the word
      "forward" and so being just as matchable by whatever process produced that excerpt. Read at
      face value, this means real PVE 9.2.4's cluster-scope (and, by the same node/cluster-shared-
      shape reasoning above, node-scope) firewall options do **not** expose `log_level_forward`,
      only vnet scope does. `internal/change.schemaFwOptionsForScope` takes this literally and
      rejects `logLevelForward` at every scope except vnet (`codeFwScopeInvalid`). If a fuller
      re-capture (`pvesh usage /cluster/firewall/options -v`, unfiltered, and the same for
      `/nodes/<node>/firewall/options`) shows `log_level_forward` after all, widen
      `schemaFwOptionsForScope` and `inventory.FwRuleset.LogLevelForward`'s population
      (`internal/inventory/ingest.go`'s `FromPVEFirewall`) accordingly — the asymmetry as currently
      modelled is a direct, literal reading of an admittedly partial capture excerpt, not something
      independently re-derived from a second source.
- [ ] **`forward` direction at guest scope.** Real PVE 9.2 was captured accepting `forward` at
      cluster, node, and vnet scope; guest scope (`/nodes/<node>/qemu/<vmid>/firewall/rules` or the
      lxc equivalent) was never captured with `pvesh usage ... -v`. `internal/change.
      schemaFwDirectionForTarget` rejects `forward` specifically at guest scope
      (`codeFwScopeInvalid`), on the reasoning that pve-firewall's FORWARD chain is a routing/host
      enforcement point, not something a single guest's own vNIC chain has — but this is inference,
      not a captured negative. Confirm with `pvesh usage /nodes/pvecube/qemu/<vmid>/firewall/rules -v`
      (or lxc) whether guest scope's `--type` enum also lists `forward`; if it does, `forward` needs
      its own real semantics defined for guest scope (not simply admitted) before the rejection is
      lifted — see item 1's own warning about not letting the validator get ahead of the resolver.
- [ ] **Vnet-scope enablement cascade.** `internal/fw.ScopeBanners`'s new `FwScopeVNet` case
      (`internal/fw/resolve.go`) assumes a vnet's forward-chain ruleset cascades from the
      datacenter-off footgun and gates on its own `enable` flag, mirroring node scope's cascade
      shape — the closest existing precedent, not a captured or otherwise confirmed behavior.
      Whether turning the datacenter firewall off on real PVE actually makes a vnet's forward-chain
      rules inert (the same way it does for node/guest scope) is unconfirmed. Confirm against a
      real vnet with `policy_forward`/rules configured, toggling `/cluster/firewall/options`'
      `enable` and observing the vnet's actual forward-chain enforcement (e.g. via `iptables`/`nft`
      output on a zone's underlying node, or observed traffic behavior).
- [ ] **What a vnet-scope forward-chain rule actually governs, end to end.** The capture proves the
      API's shape (`/cluster/sdn/vnets/{vnet}/firewall/{rules,options}`), not which traffic actually
      traverses it (guest-to-guest within the vnet? only traffic crossing the vnet's L3 gateway to
      another vnet/the outside? both?), nor how it composes with cluster-scope rules or a guest's
      own in/out chain. `internal/sim`'s path simulator (`noteVNetFirewall`,
      `internal/sim/firewall.go`) deliberately does **not** enforce vnet-scope rules for this reason
      — it only discloses that an enabled ruleset exists on the path's attached vnet
      (`CodeVNetFirewall`), the same "disclose, don't guess" treatment `CodeNodeFirewall` already
      gives node-scope host-chain rules for guest-to-guest forwarded traffic. Confirm the actual
      enforcement point/composition against real hardware before ever attempting to model vnet-scope
      verdicts in the simulator.

File these alongside T-3103's own report; item 3 (resolution-order hardware comparison) is a
separate, already-tracked line of work — see `docs/features/firewall.md` and this file's other
firewall entries for that.

## T-3104 — IPAM completion (2026-08-17)

Item 2 (PVE IPAM plugin instance CRUD) has real captured evidence for the enum
(`planning/reports/evidence/pve-9.2.4-sdn-schema.txt`'s `### usage /cluster/sdn/ipams` section) but,
like T-3102's controllers, no per-type field breakdown; item 3 (the NetBox/phpIPAM write client) has
no captured evidence at all — there is no NetBox or phpIPAM instance reachable from pvecube, and
CLAUDE.md's "capture from pvecube" discipline does not extend to a third-party system vnprox merely
talks to. None of the following is observed against real hardware:

- [ ] **Per-type field assignment for ipam instances is inferred, not captured.** Unlike the
      fabrics section's transcript (explicit "Conditional options:" groupings), the ipams section's
      `pvesh usage create` block lists `--fingerprint`/`--section`/`--token`/`--type`/`--url` flat,
      with no per-type grouping at all — not even the controllers section's own field descriptions
      to lean on (`--section`/`--token` both say "no description available"). `internal/change/
      validate_schema.go`'s `sdnIpamTypeFields`/`sdnIpamRequiredFields` and
      `internal/pvemock/sdn_ipam.go`'s mirrored maps assign url+token+section+fingerprint to
      netbox/phpipam (url+token required, section+fingerprint optional) and nothing to pve, purely
      from each field's own evident purpose ("connection config for an external system"). Confirm
      with `pvesh create /cluster/sdn/ipams --ipam test1 --type pve --url http://example.com` (want:
      rejected) and `pvesh create /cluster/sdn/ipams --ipam test2 --type netbox --url
      http://example.com` (no `--token`; want: rejected if token truly is required, accepted if it
      is not) against a real cluster, then correct both maps together if either disagrees.
- [ ] **Whether a token-less create/update is even possible for netbox/phpipam.** See the item
      above — `sdnIpamRequiredFields` currently hard-requires `token` for both external types. If
      real PVE allows creating a netbox/phpipam ipam instance without one (e.g. relying on an
      unauthenticated or IP-allowlisted external API), this model is stricter than PVE's own.
- [ ] **`sdn.ipam.update`'s type immutability.** `internal/change.SdnIpamUpdateParams` omits `Type`
      on the assumption that changing an ipam instance's type is a delete+create, mirroring
      `SdnFabricUpdateParams`'/`SdnControllerUpdateParams`' own immutability — but the capture has
      no `pvesh usage /cluster/sdn/ipams -v`'s `set`/PUT usage block at all (only `get`/`create`),
      so this is unconfirmed either way.
- [ ] **Whether `GET /cluster/sdn/ipams` on a cluster with zero configured instances actually lists
      the built-in "pve" plugin, or comes back empty.** Pre-dates T-3104 (`internal/pvemock/
      ipam.go`'s `defaultIpamID` doc comment already flagged this) but is now more consequential:
      T-3104 wired ipam instances into the live-polled inventory graph
      (`internal/collect.pollSDN` -> `inventory.FromPVESDNIpams`), so this mock's "always synthesize
      a `pve` entry when none is configured" choice now means *every* fixture without an explicit
      `sdn.ipams:` section shows a `sdn-ipam::pve` entity — a real, else-untestable claim about
      production behavior. If real PVE actually returns an empty list on a cluster with nothing
      explicitly configured, every such cluster's inventory graph gains one entity vnprox invented,
      not one PVE reported. Confirm with `pvesh get /cluster/sdn/ipams` against pvecube (which has
      never had an ipam plugin explicitly configured) and correct `effectiveIpams`'s fallback (and
      `pollSDN`'s ingestion of it) if it disagrees.
- [ ] **Whether PVE ever echoes a configured ipam plugin's token back on a read.** This task's
      entire design for item 3's production wiring (`cmd/vnproxd/ipam_external.go`'s
      `buildExternalIPAMClient`) rests on the assumption that it does not — inferred from the
      capture giving no evidence either way (no populated ipams fixture existed to observe a
      real `GET` against) and from how PVE treats other plugin secrets (the SDN DNS plugin's own
      key) as write-only. If real PVE *does* return the token, `buildExternalIPAMClient`'s
      "always nil" behavior is needlessly conservative and should read `ip.Token` directly instead.
      Confirm with `pvesh set /cluster/sdn/ipams/<id> --token <secret>` followed immediately by
      `pvesh get /cluster/sdn/ipams` against a real cluster (a synthetic/throwaway netbox entry is
      enough — no real NetBox instance is needed for this specific check, only PVE's own read
      behavior).
- [ ] **NetBox/phpIPAM API request/response shapes (`internal/ipam/netbox.go`,
      `internal/ipam/phpipam.go`).** Modeled entirely from each system's public REST API
      documentation, never exercised against a live instance of either. Specific points flagged in
      each file's own package doc comment: NetBox's `/32`-qualified address convention (a real
      deployment may already hold the same addresses at a different prefix length, which this
      client's own id-lookup-by-exact-address would miss on update/delete); phpIPAM's
      authentication (this client sends the configured token as a static `token` header on every
      request, bypassing phpIPAM's documented session-exchange flow — unconfirmed against a real
      App API configuration); and phpIPAM's lack of a flat "list every address" endpoint (this
      client fans out one request per subnet from `/subnets/`, aggregating — correct per the
      documented shape, but unverified against real per-app subnet visibility scoping). phpIPAM
      also needs its own "app id" (a phpIPAM-side identifier PVE's ipam config has no field for),
      a second, independent blocker from the token problem above.
- [ ] **What deleting a referenced ipam instance actually does to a zone on real PVE.**
      `checkSdnIpamDeletable` (mirroring `checkSdnControllerDeletable`) blocks the *changeset*, but
      whether real PVE's own server-side validation also rejects the raw API call
      (`DELETE /cluster/sdn/ipams/{id}` while a zone's `ipam` field still names it) — or silently
      leaves the zone with a dangling reference — is unconfirmed.

File these under `T-3201` per this file's own convention (cross-node/hardware-only checks), except
the NetBox/phpIPAM API-shape entry, which needs a real NetBox/phpIPAM instance rather than PVE
hardware and so does not fit that convention cleanly — flagged here regardless rather than dropped
for lack of a clean bucket.
**`T-3201` status (2026-08-18): not attempted** — same reasoning as the Fabrics/Controllers
sections above; no ipam instance was configured against either real node this session.

## Debt sweep item 8 — `install.sh` multi-node PVE token copy (2026-08-19)

Fixed: `packaging/install.sh`'s SSH rollout (step 8) never copied the cluster-wide PVE API token
(`/etc/vnprox/keys/pve-token`) to nodes 2+, so every node after the first came up unable to
authenticate to PVE — `docs/deployment.md` documented the gap but `install.sh` was never changed to
close it. This change adds `copy_pve_token_to_node` (scp -p straight to the final `0700 root:root`
path, `chown root:root`/`chmod 0600` reasserted explicitly, then `systemctl restart vnprox`), called
for every node the SSH rollout succeeds on, with a manual fallback recipe printed when it can't run.

- [x] **The container-simulated 3-node rollout — CLOSED, `T-3705`, 2026-08-23.**
      `packaging/test/cluster-ssh.sh` ran to completion after 5 earlier attempts failed inside
      `make build` itself (root cause: two concurrent agent processes both running `make build`
      against the same `web/node_modules` at once, plus a `PATH` gap for a non-interactive shell —
      not a defect in the harness; see `planning/reports/evidence/T-2410-cluster-ssh-attempt-
      2026-08-23.txt`). The clean run: `ALL CHECKS PASSED`, including the exact check this item
      asks for — `install.sh step 8 copied pve1's PVE API token to pve2 and pve3 (debt sweep item
      8, 2026-08-19)` — plus the cluster-secret replication and same-port checks. Full transcript:
      `planning/reports/evidence/T-2410-cluster-ssh-pass-2026-08-23.log` (1,026 lines).
      **Still not the same claim as a real multi-node PVE cluster**: three podman containers with
      fake `pveversion`/`pvecm` stubs (per the script's own header) is a real SSH rollout onto a
      real second/third `sshd`, but not real PVE, and this project has no way to re-run
      `install.sh` against a genuine second PVE node — `vnprox-dev`'s `pve001` isn't root-accessible
      to us, and re-running install against `pvecube` itself would mutate the live deployed
      product. **One green run, not the three repeated runs a flake-proofing pass would want**
      (T-2410's own card opened on "green locally... red on the runner" — a single pass doesn't
      rule out the same intermittency recurring). **Blocked (residual): needs root on `pve001`
      (a real second PVE node to onboard) or the T-3704 lab; the container-level claim itself is
      now closed.**

## T-3503 — NIC media type: the fibre / direct-attach branch (2026-08-20)

The Switch faceplate now draws a port from its reported media type (`PORT_*` out of
`ETHTOOL_GSET`, plumbed host → inventory → topology), so a copper port renders as an RJ45 jack, a
fibre or direct-attach port as an SFP cage, and an unreported one as a visibly distinct "no reading"
body. `planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt` is the read-only transcript the
mapping was built from.

- [ ] **Only the copper branch has been exercised against real hardware.** All four NICs on
      `pvecube` are `Port: Twisted Pair` (`igc`), so `PORT_TP` is the only value the live ioctl has
      ever returned here. `PORT_FIBRE` and `PORT_DA` — and with them the SFP cage rendering, and the
      `speedMarking` output above 1G — are covered by unit fixtures only
      (`internal/topology/project_media_test.go`, `web/src/topology/portMedia.test.ts`,
      `SwitchFaceplate.device.test.tsx`). This is the same class of limit as the cluster-behaviour
      entries above and for the same reason: the one available node cannot produce the input. What
      to confirm on hardware with an SFP+/DAC port: `ethtool <if>` reports `Port: FIBRE` (or
      `Direct Attach Copper`), `GET /topology` carries `"mediaPort": "fibre"` (or `"da"`), and the
      faceplate draws a cage rather than a jack.
- [ ] **A NIC whose driver reports `PORT_NONE`/`PORT_OTHER`, or fails `ETHTOOL_GSET` outright.**
      Both map to the "no reading" body by design ("never guessed"), but no driver on this node does
      either, so the fallback has never been seen on real hardware. Virtio NICs inside a VM are the
      likeliest real source and are worth checking opportunistically.
- [ ] **A peer node's ports.** Media type is a host-netlink-only fact (`ownershipRules`), so a node
      this daemon does not poll directly carries none and every one of its ports draws as unknown
      media. That is the intended, honest behaviour — but whether it reads as *informative* rather
      than *broken* to an operator looking at a multi-node map is a judgement no single-node
      environment can make. Needs a real cluster.

## T-3604 — Tier 2 service start: everything except the single-node success path (2026-08-21)

The success path has now been closed against real hardware: `frr` was started on `pvecube` through
the deployed SPA's own button, the audit row landed, and the finding cleared on the next collection.
`planning/reports/evidence/pve-9.2.4-tier2-service-start-live.txt` is the transcript. What that run
could *not* reach:

- [ ] **Fan-out to a peer.** The one exercised call named the node the daemon runs on, so the peer
      round-trip — the request crossing to a node this daemon does not own, and the result coming
      back — is still entirely unobserved. `vnprox-dev` now has a second node (`pve001`), so this is
      no longer blocked by node count — but starting/stopping a real systemd unit on `pve001` is a
      live mutation to a node this project has no authorization to change (`T-3705`, 2026-08-23).
      **Blocked: needs root on `pve001`, which this project does not have.**
- [ ] **Partial success across a cluster.** A finding that names several nodes offers one button;
      what the operator sees when it succeeds on two nodes and fails on a third is covered by
      `NodeResultsList`'s unit tests and by nothing else. Exercising this for real means a
      service-start action that touches both nodes at once, including `pve001` — same constraint as
      immediately above. **Blocked: needs root on `pve001`, which this project does not have.**
- [ ] **A node unreachable at press time.** Distinct from "systemd refused", which *is* now
      observed (id 142, the dnsmasq refusal). An unreachable peer fails in the transport rather
      than in the unit, and that error text has never been rendered from a real failure.
- [ ] **Every allow-listed unit except `frr`.** `frr` was the only entry `pvecube` could offer in
      an installed-and-stopped state. The rest are exercised against the mock only — and the
      dnsmasq false positive is a standing reminder that the mock agreeing with the code proves
      nothing about the node.

## T-3701 — SDN zone status: per-node endpoint, and a real cross-node divergence (2026-08-23)

The client now calls the endpoint PVE 9.2.4 actually implements (`GET /nodes/{node}/sdn/zones`,
per-node — `internal/pve/sdn.go`'s `ListNodeSDNZoneStatus`), reconciled against each zone's declared
node membership by `pve.ReconcileSDNZoneStatus`. CLAUDE.md's "one real node, no cluster" line was
out of date during this task: `pvecube` has been a member of a quorate two-node corosync cluster
(`vnprox-dev`, `pve001` at 192.168.1.7) since 2026-08-18 —
`planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt` is the read-only transcript. That
changed what this task could verify directly, not just what it had to flag:

- **Verified live, not flagged.** Cross-node reads from `pvecube` work
  (`pvesh get /nodes/pve001/sdn/zones`), and the two nodes disagree on this exact endpoint right
  now: `pvecube` reports `labz` `status: error`; `pve001` answers the identical call with an empty
  array `[]` and only a human-readable "local sdn network configuration is not yet generated"
  warning on stderr — no error status, no per-entry `node` field, no `zone: labz` row at all. This
  is real production behaviour, not synthesized for this task, and it is exactly the shape
  `ReconcileSDNZoneStatus`'s "unknown" synthesis exists to handle (a declared member node PVE had
  nothing to say for a zone is not the same fact as that node being healthy). `internal/pvemock`'s
  new `SDNZonesUnavailable` per-node flag reproduces this exact response (empty array,
  unconditionally, independent of `SDNZoneStatusFail`/bridge state) and is exercised by
  `internal/pvemock/server_test.go`'s `TestSDNZoneStatus_UnavailableNodeAnswersEmpty` and
  `internal/pve/integration_test.go`'s `TestSDNZoneStatus_ReconcileAcrossDivergentNodes` — genuine
  cross-node divergence against a real mock server, not two hand-rolled fakes agreeing with each
  other.
- [ ] **Whether an empty `SDNZone.Nodes` list means "every cluster node" on real PVE.** `pvesh
      usage /cluster/sdn/zones/labz -v`'s `--nodes` description is just "List of cluster node
      names.", with no stated default. `ReconcileSDNZoneStatus` (and this mock's own
      `zoneAssignedToNode`, unchanged by this task) assume — based on Proxmox's general documented
      SDN zone behaviour, not anything observed on this cluster — that no `--nodes` restriction
      deploys a zone cluster-wide. Confirming this needs creating an unrestricted zone, which is a
      mutating write against `vnprox-dev`'s two live nodes and out of scope for a read-only task.
- [ ] **A cluster of three or more nodes, for majority-vs-minority disagreement.** Two nodes can
      only ever show "they differ"; they cannot show which answer an operator should trust more
      when, say, two of three agree and one doesn't. `ReconcileSDNZoneStatus` reports every node's
      status independently with no voting/consensus logic (deliberately — PVE's own per-node
      realization state has no notion of a "majority" answer being more correct), so this is a
      product-behaviour question for a future task, not a defect in this one, but it remains
      unverified beyond two nodes.
- [ ] **A zone whose member list spans nodes in genuinely different health states beyond
      error/unavailable** — e.g. one node mid-apply (`pending`) while another already shows
      `error` for the same zone, both for real rather than injected. `pve3`'s missing-bridge
      scenario (`messy-brownfield.yaml`, reused for this task's mock-level tests) and the
      `SDNZoneStatusFail`/`SDNZonesUnavailable` injection flags cover the mechanism, but a
      genuinely-observed three-way split (ok / pending / error, or ok / error / unknown) on live
      hardware has not been captured.
