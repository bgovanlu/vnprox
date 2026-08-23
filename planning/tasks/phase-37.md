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
project.

> **Amended 2026-08-23:** the second node already exists and is already clustered. `pvecube` and
> `pve001` have formed the quorate corosync cluster `vnprox-dev` since 2026-08-18 — five days
> before this matrix was written. Nothing needs to be built to unblock the 17. See the amendment
> under Wave 2, and `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt`.
>
> This is worth stating plainly as a process finding, because it is the same failure the SDN-zone
> defect came from and that `CLAUDE.md` already warns about: **the limit was inferred from project
> documentation rather than observed on the node.** One `pvecm status` would have answered it at
> any point in the last five days. The audit that produced this phase did not run it.

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

Not one of these needs code. All 17 were recorded as blocked on the same missing thing: **there is
no second node.**

> ### ⚠️ Amended 2026-08-23 — the premise was false
>
> There is a second node, and it is clustered. `pvecube` has been a member of a **quorate two-node
> corosync cluster** named `vnprox-dev` since **2026-08-18**, with `pve001` at 192.168.1.7.
> `pvecm status` reports `Quorate: Yes`, both nodes online, and cross-node `pvesh` reads work from
> pvecube today. Evidence: `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt`.
>
> This invalidates the premise of `CLAUDE.md`'s "one real node, no cluster" line, of the
> "Shipped, unproven" column in `docs/audit-matrix-2026-08-23.md`, and of the 156 open items in
> `needs-hardware-validation.md` — all of which *inferred* the limit rather than checking it. The
> matrix was written five days after the cluster was formed.
>
> **T-3704 is therefore rewritten below, and heavily reduced.** Most of Wave 2 no longer waits on
> anything.

### T-3704 · Use the cluster that exists; build a lab only for what it cannot answer
**model:** sonnet-5 · **size:** S (was M) · **depends:** — · **unblocks:** most of Wave 2

The original card proposed building two nested PVE guests on pvecube to obtain a second node. That
is now redundant for every *non-destructive* purpose, and building it anyway would burn 8 GB of RAM
and 64 GB of disk reproducing something already running.

**What `vnprox-dev` can answer, and should be used for immediately:** peer round-trips, fan-out,
cross-node validation, drift between nodes, mixed-version peering, per-node API divergence. It is
already demonstrating the last two without being asked — see the SDN-zone split in T-3701 below,
and `pve001` running an older vnproxd than pvecube.

**What it cannot answer, and what the lab is now scoped to:**
- **Anything destructive.** `vnprox-dev` is the user's live cluster. Partition behaviour,
  quorum loss, killing a node mid-rollback, deliberately corrupting drift — none of that may be
  done here.
- **Anything on `pve001` requiring root.** This project has **no SSH credentials for `pve001` and
  no authorisation to modify it.** It is observable through pvecube's `pvesh` and reachable as a
  vnprox peer; it is not ours to change or upgrade. Any plan that depends on deploying to it is
  blocked, not merely slow — see T-3703's compatibility constraint, which this fact drove.
- **Quorum with a survivor**, which needs three nodes. Two cannot demonstrate it.
- **Physical behaviour** — no real NICs in a nested guest, so no bond failover, no LACP against a
  real switch, no media-type branch beyond `PORT_TP`. Unchanged from the original card.

**Deliverables (reduced)**
- A build/teardown script under `scripts/` for a disposable nested lab, used **only** for the
  destructive subset above. It is a fixture, not an environment. `proxmox-ve_9.2-1.iso` is already
  staged in `/var/lib/vz/template/iso/` on pvecube (1 706 178 560 bytes, verified against the
  publisher's `content-length`).
- `docs/development.md` gains a section covering both: how to talk to `vnprox-dev`, and how to
  stand up the disposable lab for the cases that must not touch it.
- Correct `CLAUDE.md`'s "one real node, no cluster" line. It is load-bearing — it is the reason 17
  features sat unproven — and leaving it stale would let the same mistake recur.

**Still do NOT cluster pvecube with anything new**, and do not alter `vnprox-dev`'s membership.
It is a live cluster running the deployed product.

### T-3705 · Run the blocked register against the real cluster
**model:** sonnet-5 · **size:** L · **depends:** T-3704 only for the destructive subset ·
**proves:** T-301, T-303, T-801, T-1101,
T-1102, T-1201, T-1203, T-1407, T-1803, T-1906, T-2001, T-2303, T-2410, T-2602, T-2703, T-2902,
T-3201

**Deliverables**
- Work `planning/reports/needs-hardware-validation.md` top to bottom against `vnprox-dev`, closing
  each item with a transcript in `planning/reports/evidence/` or restating precisely why it cannot
  be answered. Read-only and non-destructive items can be closed **now**; the destructive subset
  waits for T-3704's lab.
- Re-run T-3702's acceptance criteria 2 and 3 here. **They need a deployment, not a new node** —
  `pve001` is already peered and already failing with the exact signature the fix removes
  (`consecutive_failures: 2382`, `last_success` never set). This is the first real peer round-trip
  the project has ever had.
- `T-2410`'s `cluster-ssh` packaging job, which has never run.

**Acceptance criteria**
1. Every one of the 17 rows moves to `Live` or to a *stated* reason it cannot.
2. The open count in `needs-hardware-validation.md` falls, and every remaining item says which of
   "needs real NICs" / "needs three+ nodes" / "needs a physical switch" / "needs root on `pve001`,
   which we do not have" / "is destructive, needs the T-3704 lab" blocks it. "Needs hardware" on
   its own is what let this sit for months — and it was not even true.

**Deploy ordering, which matters.** T-3703 changes the peer signing format and `pve001` cannot be
upgraded by us. Read T-3703's compatibility note before deploying anything to pvecube, and verify
after deployment that pvecube→`pve001` polling still succeeds. If it does not, T-3703 has
reintroduced T-3702's failure through a different mechanism and must be rolled back.

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

Original:

```
Wave 1  T-3701 ──┐                     (independent, ship first)
        T-3702 ──┼── verifiable only after T-3704
        T-3703 ──┘
Wave 2  T-3704 ───► T-3705 ───► closes the 17, and Wave 1's ACs
                └──► T-3706 ───► closes 7 of the 29
Wave 3  T-3707  (owner decision, no dependency — can start today)
        T-3708  (independent)
```

Amended 2026-08-23, once `vnprox-dev` was found to exist. The critical path is now a **deploy**,
not a build:

```
Wave 1  T-3701 ──┐   verifiable against vnprox-dev NOW (the two nodes
        T-3702 ──┼──   already disagree on /nodes/{node}/sdn/zones)
        T-3703 ──┘
           │
           └──► DEPLOY to pvecube ──► closes T-3702 AC2+AC3
                    │                  (watch pve001 polling; see T-3703)
Wave 2  T-3705 ─────┴──► closes the 17 and most of the 156, no lab needed
        T-3704  reduced: a lab for the destructive subset only
        T-3706 ───► closes 7 of the 29   (needs the lab — flows must not
                                          be switched on over live traffic)
Wave 3  T-3707  (owner decision, no dependency — can start today)
        T-3708  (independent)
```

**The blocker is no longer hardware; it is authorisation.** `pve001` is reachable and clustered but
this project has no root on it, so anything requiring a change *on* that node — including upgrading
its vnproxd — is blocked on the owner, not on engineering.

`T-3707` blocks on nobody and gates eleven features: **ask it first**, even though it ships last.

## Explicitly not in this phase

- **Enabling anything on pvecube beyond the two fixes.** The lab is where features get switched on.
  pvecube runs the deployed product and is the user's live host.
- **New features.** Every card here makes something already written actually work.
- **A third node.** Two proves the peer protocol; three proves quorum behaviour, and nothing in the
  matrix currently claims quorum.
