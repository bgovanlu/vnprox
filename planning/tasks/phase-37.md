# Phase 37 — Close the gap between "shipped" and "working"

**Arc:** *the audit found 51 features that are not doing their job; this is the order to fix them in.*

**Source:** `docs/audit-matrix-2026-08-23.md` (236 cards, four status axes) and the three defects in
`planning/reports/audit-2026-08-21-*.md` / `planning/reports/evidence/pve-9.2.4-sdn-zone-status.txt`.

## Premise

Implementation, documentation and test coverage are effectively complete — 230/236, 236/236,
230/236. The audit's whole signal is in the fourth axis:

| | count | what it means |
|---|---|---|
| Live | 185 | reachable and exercised on pvecube |
| **Degraded** | **5** | shipped, reachable, and **broken against real PVE** |
| **Shipped, unproven** | **17** | cannot be exercised on one node |
| **Shipped, inert** | **29** | in the binary, switched off by configuration |

Those three groups need three different kinds of work, and only one of them is "write code".
Ordering them by severity alone would be wrong, because the cheapest action in the phase — a
nested two-node lab — is what makes half of Wave 1's fixes *verifiable*. So the sequence below is
ordered by **severity first, and by what unblocks what second**, with that coupling made explicit.

**The one number that should drive scheduling:** a second node unblocks 17 features **and** the 156
open items in `planning/reports/needs-hardware-validation.md`, and it is the only thing that can
verify Wave 1's peer fixes. It has been treated as "needs hardware we don't have" for the whole
project. It does not: `pvecube` reports `vmx` with `kvm_intel.nested=Y`, 8 cores, 12 GB free and
97 GB on `local-lvm`. See T-3704.

---

## Wave 1 — the five Degraded features (P0)

These are the only entries in the matrix where vnprox is **lying to an operator**: it renders a
feature that does not work, on the node it is installed on. Everything else in this phase is a
deliberate choice or a limit of the lab. These are defects.

### T-3701 · SDN zone status: call the endpoint PVE actually has
**model:** sonnet-5 · **size:** M · **depends:** — · **already carded:**
`planning/tasks/T-3701-sdn-zone-status.md` · **fixes:** T-401, T-402

Card exists in full; it is folded into this phase rather than rewritten. Summary: vnprox calls
`GET /cluster/sdn/zones/{zone}/status`, which PVE 9.2.4 does not implement (501, 8 540 retries per
day). The real endpoint is `GET /nodes/{node}/sdn/zones` — per-node, not per-zone, so the fix
inverts the call's axis rather than editing a URL. `internal/pvemock` **serves the invented route**
and must have it deleted, not merely supplemented.

**Why it is first:** `labz` is in `status: error` on pvecube right now and the product cannot see
it. This is the only defect in the phase that hides a real fault in the user's network.

### T-3702 · Peer responses are cancelled before their body is read
**model:** sonnet-5 · **size:** S · **depends:** — · **report:**
`planning/reports/audit-2026-08-21-peer-body-cancel.md` · **fixes:** T-301, T-302, T-303

`internal/peer/client.go:223` opens a per-request context with `defer cancel()` and returns the
`*http.Response` with its body unread; `decodeInto` reads it afterwards, by which time the context
is dead. 17 197 warnings/24h, the largest class in the log, and `host [pve001]` has **never once**
succeeded in the deployed health endpoint.

**Deliverables**
- Remove the `reqCtx`/`cancel` pair; build the request with the caller's `ctx` so caller
  cancellation still propagates. `http.Client{Timeout: …}` at line 139 already bounds body reads
  and is the correct construct — **the fix is a deletion**, and the diff should be smaller than
  this card.
- A regression test that fails on the current code. The existing peer tests cannot catch this:
  they use small fixtures on loopback, where the transport buffers the whole response before
  `cancel()` lands and the race always resolves benignly. The reproduction in the report
  (1 and 100 entries pass, 5 000 and 50 000 fail) shows the shape — a body large enough to still be
  streaming, or an explicit synchronisation point between `do()` returning and the read.

**Acceptance criteria**
1. The new test fails against `HEAD~1` and passes after. Demonstrate both.
2. On the deployed node, `host [pve001]` reports a non-zero `last_success` — verified against
   `GET /api/v1/health`, not asserted from the mock.
3. `collect: peer host poll failed … context canceled` no longer appears in the journal.

### T-3703 · A repeated peer GET is not a replay
**model:** sonnet-5 · **size:** S · **depends:** — · **report:**
`planning/reports/audit-2026-08-21-peer-replay.md`

The peer signature covers `(method, requestURI, sha256(body), ts)` with **no nonce**, and `ts` is
unix *seconds*. Two identical polls inside one second are byte-identical, so the second is rejected:
2 885 legitimate reads dropped per day. `middleware.go:21` argues the collision is
"cryptographically infeasible", which is true of two *different* requests and silent on the same
request recurring — the normal behaviour of a poller.

**Deliverables**
- A random 128-bit nonce in the canonical string, sent as a header; the replay cache keys on the
  nonce instead of the signature. This is the standard construction and it makes the existing
  comment's reasoning actually true.
- Correct that comment. It is load-bearing: it is why nobody looked here.
- A test that issues the *same* signed GET twice inside one second and requires both to be accepted,
  and a second that replays a captured nonce and requires rejection. Both directions, or the change
  proves nothing.

**Rejected alternatives, recorded so they are not re-proposed.** Exempting idempotent reads works
and is smaller, but leaves an asymmetry someone must remember. Millisecond timestamps only narrow
the window — two polls can share a millisecond — so it is a mitigation, not a fix.

**Note:** T-3703 is not one of the five Degraded rows, because it degrades reliability rather than a
named feature. It ships in this wave because it is in the same package as T-3702 and has the same
verification need.

---

## Wave 2 — the 17 Unproven features (P1)

Not one of these needs code. All 17 are blocked on the same missing thing, and so are Wave 1's peer
fixes: **there is no second node.**

### T-3704 · A disposable two-node PVE cluster, nested on pvecube
**model:** sonnet-5 · **size:** M · **depends:** — · **unblocks:** all of Wave 2, T-3702's AC2,
and much of `needs-hardware-validation.md`

**This is the highest-leverage card in the phase.** The project has recorded "needs a real cluster"
against 17 features and 156 validation items for months, on the assumption that it needs hardware
nobody has. The hardware is already here:

```
pvecube: 8 cores · 15 GB RAM (12 free) · vmx · kvm_intel.nested = Y
         local 49 GB free · local-lvm 97 GB free · 3 guests, all stopped
```

**Deliverables**
- Two nested PVE guests (`pve-lab-1`, `pve-lab-2`), 2 vCPU / 4 GB / 32 GB each, clustered **with
  each other**. vnproxd deployed to `pve-lab-1`, polling `pve-lab-2` as a peer.
- A build/teardown script under `scripts/`, so the lab is reproducible and disposable rather than a
  pet. It is a fixture, not an environment.
- `docs/development.md` gains a section on standing it up.

**Do NOT cluster pvecube itself.** Creating a PVE cluster on it converts `/etc/pve` to a
corosync-backed filesystem on a live host running the deployed product, and is not reversible in
any pleasant way. Two disposable guests clustered with each other exercise every peer, fan-out and
federation path without touching the machine the user depends on. **This constraint is the point of
the card, not a footnote.**

**Honest limits, so the next agent does not overclaim.** A nested lab proves *protocol* — peer
round-trips, fan-out, distributed rollback, drift between nodes, cross-node validation. It does not
prove *physical* behaviour: no real NICs, so no bond failover, no LACP against a real switch, no
media-type branch beyond `PORT_TP`. And a two-node corosync cluster has no quorum without
`two_node: 1` or a qdevice; whichever is chosen must be written down, because it changes what
partition behaviour the lab can demonstrate.

### T-3705 · Run the blocked register against the lab
**model:** sonnet-5 · **size:** L · **depends:** T-3704 · **proves:** T-301, T-303, T-801, T-1101,
T-1102, T-1201, T-1203, T-1407, T-1803, T-1906, T-2001, T-2303, T-2410, T-2602, T-2703, T-2902,
T-3201

**Deliverables**
- Work `planning/reports/needs-hardware-validation.md` top to bottom against the lab, closing each
  item with a transcript in `planning/reports/evidence/` or restating precisely why the nested lab
  cannot answer it.
- Re-run T-3702's acceptance criterion 2 here: it is the first real peer round-trip the project has
  ever had, and the fix shipped in Wave 1 will have been verified only by a unit test until now.
- `T-2410`'s `cluster-ssh` packaging job, which has never run.

**Acceptance criteria**
1. Every one of the 17 rows moves to `Live` or to a *stated* reason it cannot.
2. The open count in `needs-hardware-validation.md` falls, and every remaining item says which of
   "needs real NICs" / "needs three+ nodes" / "needs a physical switch" blocks it. "Needs hardware"
   on its own is what let this sit for months.

---

## Wave 3 — the 29 Inert features (P2)

**Most of these are correctly off, and the deliverable is a decision, not a switch.** The failure
mode here is a well-meaning agent enabling flow ingestion and a hosted registry on someone's live
node because a matrix said "inert". Grouped by what each actually needs:

| Blocker | n | Disposition |
|---|---|---|
| flow ingestion off | 4 | **Enable in the lab** — T-3706 |
| needs a seeded flow corpus | 2 | follows from the above |
| conntrack sampling off | 1 | **Enable** — one config key, no new capability needed |
| registry / hub unset | 7 | **Decide** — needs a hosted service that does not exist |
| demo mode | 3 | correctly off; it is a separate `--demo` daemon |
| MCP | 4 | correctly off until a client is attached |
| Kubernetes | 2 | needs a cluster the user does not have |
| WireGuard | 2 | needs a tunnel; pairs naturally with T-3704's lab |
| Grafana | 1 | needs an instance |
| Ceph | 1 | installed but unconfigured (`ceph.target` up, no `ceph.conf`) |
| plugin SDK | 1 | needs a third-party plugin |

### T-3706 · Turn on the flow stack, in the lab, and see what breaks
**model:** sonnet-5 · **size:** M · **depends:** T-3704 · **unblocks:** T-1002, T-1003, T-1004,
T-1305, T-1601, T-1602, T-1603

Seven features — a quarter of the inert set — come alive from one group of config keys, and not one
of them has ever run against real traffic. `T-1602`/`T-1603`'s two `test.skip`s in
`microseg.spec.ts` are blocked on exactly this: a seeded flow corpus, with no `[flows]` dev-fixture
loader to produce one.

**Deliverables**
- `conntrack_sampling_enabled`, then sFlow/NetFlow/IPFIX, on the lab — **not on pvecube**, until
  each has been seen to behave.
- The `[flows]` dev-fixture loader `microseg.spec.ts` names, so AC4/AC5 can be un-skipped.
- Remove those two `test.skip`s, or give them an expiry in `quarantine.json`. They currently sit
  outside the one mechanism this repo has for time-boxing disabled tests.

**Acceptance criteria:** real flows appear in the explorer against lab traffic; the two microseg
tests run; the SDN-zone-status lesson is applied — anything the mock asserts about flow records is
checked against what the node actually emits.

### T-3707 · Decide the hosted-service group, in writing
**model:** strong · **size:** S · **depends:** — · **covers:** the 7 registry/hub cards, the 3 demo
cards, T-2503 telemetry

Eleven features are inert because they need a service nobody has stood up: a signed blueprint/plugin
registry, a hosted demo, a telemetry endpoint. That is a **product decision**, and it has been
implicitly deferred by never being asked.

**Deliverable:** one page in `planning/` recording, per group, whether it is *going to exist*. If
yes, it earns a card. If no, the features are marked "shipped, deliberately unhosted" in the matrix
and stop being counted as a gap. Either outcome is fine; the current state — eleven features in
permanent limbo — is not.

**This is the one card in the phase the owner must answer rather than an agent.**

### T-3708 · Give the unit suite the flake visibility the e2e suite has
**model:** sonnet-5 · **size:** M · **depends:** —

Found while running the audit: the e2e suite has `cmd/e2egate`, a quarantine with hard expiries and
a 20-run trend. The **2 278-test vitest suite, which gates every push, has none of it.**
`TenantsPanel.test.tsx` refused a push, then passed 3/3 alone and 295/295 in-suite; the cause was
Testing Library's 1 000 ms default against `make ci`'s concurrent load. Fixed in `2cd48367`, but
nothing records that the test is load-sensitive, so the next one will be diagnosed from scratch.

**Deliverable:** vitest run history and a flake trend, in the shape `cmd/e2egate` already
establishes. Do not invent a second mechanism.

---

## Sequencing

```
Wave 1  T-3701 ──┐                     (independent, ship first)
        T-3702 ──┼── verifiable only after T-3704
        T-3703 ──┘
Wave 2  T-3704 ───► T-3705 ───► closes the 17, and Wave 1's ACs
                └──► T-3706 ───► closes 7 of the 29
Wave 3  T-3707  (owner decision, no dependency — can start today)
        T-3708  (independent)
```

`T-3707` blocks on nobody and gates eleven features: **ask it first**, even though it ships last.

## Explicitly not in this phase

- **Enabling anything on pvecube beyond the two fixes.** The lab is where features get switched on.
  pvecube runs the deployed product and is the user's live host.
- **New features.** Every card here makes something already written actually work.
- **A third node.** Two proves the peer protocol; three proves quorum behaviour, and nothing in the
  matrix currently claims quorum.
