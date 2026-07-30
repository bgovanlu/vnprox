# T-1407 follow-ups · linkage reconciliation + wizard UI

Closes the two items `planning/reports/T-1407.md` left in its "Notes for the next agent"
section. No new task card — these are that card's own deferred tail, cut as `v3.0.4`.
No schema change (still version 32).

## The problem

T-1407 shipped `clusters.wg_tunnel_id`: an optional linkage marking an attached
federation cluster as reachable over a specific vnprox-managed WireGuard tunnel, so a
down tunnel collapses into one `tunnel_down_peer_unreachable` finding instead of three
per-surface `partial`/`failedClusters` flags. But `wireguard_peers.cluster_id` — shipped
dormant back in T-1401 (migration `0016_wireguard.sql`) as "links a federation-managed
internal peer" — records the *same fact from the other end*, and nothing wrote it. Two
columns, one fact, no rule about which wins: exactly the drift T-1407's report flagged.

Second gap: the linkage had no UI at all. `PUT /federation/clusters/{id}` was the only
way to set it, and there is no federation-cluster editor screen in the SPA.

## What was built

### 1. One effective linkage, resolved on read

The two columns record the same relationship at different granularities — a *peer* **is**
a cluster (per-peer, and one tunnel can carry several); a *cluster* is **reached over** a
tunnel (per-cluster). That's a real distinction, so they aren't merged. Instead there is
now exactly one *effective* answer, resolved in `internal/federation.Service`:

- **Explicit wins.** A non-empty `clusters.wg_tunnel_id` is the operator's deliberate
  override and is used as-is — including for a tunnel with no peer tagged for this
  cluster at all (an operator linking a tunnel whose far side they modeled some other way).
- **Otherwise derive.** The linkage falls back to the tunnel carrying a peer annotated
  with this cluster id.
- `Cluster.WgTunnelSource` (`"explicit"` | `"peer"` | `""`) reports which, so a reader can
  tell an override from a derived link.

Resolution happens in `Get`/`List`/`Update`, so **every downstream consumer picks it up
for free and none of them could be made to disagree**: `Aggregator.splitTunnelDown` and
`internal/findings`' `tunnel_down_peer_unreachable` producer both read
`Cluster.WgTunnelID` and were not touched. `Add` deliberately skips resolution — a
just-minted ULID cannot have a peer tagged for it yet.

The seam is `federation.TunnelLinker` (one method,
`TunnelIDForCluster(ctx, clusterID) (string, error)`), satisfied by
`*store.WireGuardRepo` and wired at the composition root. It is **optional**: a nil
linker reproduces T-1407's shipped explicit-only behaviour exactly, which is what keeps
every existing federation test honest.

Two rules that make the derived link safe to rely on:

- **Deterministic under ambiguity.** If the same cluster is tagged on peers of two
  tunnels (an operator mid-migration), the lowest tunnel id wins —
  `ORDER BY tunnel_id ASC LIMIT 1`. Arbitrary, but stable: the derived link never flaps
  between reads. An operator who needs the other one sets the explicit override.
- **Fail-open.** A linker error is logged at debug and swallowed; the cluster degrades to
  "not tunnel-linked". This is the same direction every other T-1407 path already
  takes — a linkage problem must never hide a cluster's data or break a registry read.
  (Note this direction: a *false negative* costs you the collapsed finding and gets you
  T-1201's ordinary unreachable handling; a false positive would silently suppress a
  cluster's data across three surfaces. Only the former is acceptable.)

The write path stays one-directional and inside the change engine: `PUT` writes only the
explicit column, never the peer annotation. So **clearing `wgTunnelId` with `""` does not
necessarily unlink a cluster** — if a tagged peer still exists it comes back linked,
sourced `"peer"`. Undoing a peer-derived link means retiring or retagging that peer
through an ordinary `wg.peer.*` changeset. This is documented on `Service.Update`, in
`docs/api.md`, and asserted by a test, because it is the one genuinely surprising
consequence of the design.

### 2. The UI: tag the peer, don't edit the cluster

The wizard's *Other side* step gains an optional **Federated cluster** select. Choosing a
cluster sets `clusterId` on the existing `wg.peer.add` op — **no new op, no new route, no
extra request**. The linkage therefore lands if and only if the changeset that creates the
tunnel is actually applied, which is the whole reason it rides the op rather than a
side-channel `PUT /federation/clusters/{id}` at draft time: a wizard that wrote the
registry directly would link a cluster to a tunnel that does not exist yet, and would
leave that write behind if the operator discarded the draft. Both invariants
(`CLAUDE.md`'s "never apply network changes outside the change engine", T-1402 AC4's "no
mutating route but the one `POST /changesets`") hold unchanged — there is a test asserting
exactly one mutation is issued with the field set.

Supporting pieces: `fetchFederationClusters()` / `useFederationClustersQuery()` (404 →
empty, matching the existing federation-read convention, so a single-cluster deployment or
a session without `netRead` sees a disabled select with an explanatory placeholder rather
than an error), and an already-linked marker in the option label so an operator doesn't
silently re-point a cluster at a second tunnel.

## Tested

`make check` green — gofmt, `go vet`, golangci-lint (0 issues), full Go suite,
199 frontend test files / 1329 tests, govulncheck clean. The `npm audit --audit-level=high`
step fails on transitive advisories (`brace-expansion`, `postcss`, `dompurify` via
`monaco-editor`, `react-router`); verified identical on `v3.0.3` with the working tree
stashed, so it is pre-existing and unrelated to this change.

New tests:

- `store`: `TestWireGuardRepo_TunnelIDForCluster` — untagged peers never match, `''`
  never matches (it is also the column default), a tagged peer resolves, two tunnels
  resolve to the lowest id, and removing the winning peer / cascade-deleting the last
  tagged tunnel falls back and then unlinks. Tunnels are inserted out of id order so
  "lowest wins" can't pass on insertion order.
- `federation`: `TestService_PeerDerivedLinkage`, `TestService_ExplicitLinkageWins`
  (including the clear-falls-back-to-derived case), `TestService_LinkerErrorDegradesToUnlinked`,
  `TestService_NilLinkerIsShippedBehaviour`.
- `api`: `TestFederationRoutes_WgTunnelSource` — GET reports a peer-derived link, PUT
  flips it to explicit, an unlinked cluster emits neither field.
- `web`: two `ConnectClustersWizard` cases (tagging rides the one changeset; no clusters
  attached ⇒ select disabled and no `clusterId` on the wire) and two `wizardOps` cases.

## Deviations

None from the follow-up notes as written. One judgement call worth naming: T-1407's report
offered "make one derive from the other, **or** clearly document why both need to exist
independently". This took the first option but implemented it as **read-side** derivation
rather than a write-time reconciliation (an apply-time hook that stamped
`clusters.wg_tunnel_id` when a tagged peer is applied). Write-time was rejected because
`wg.peer.add` applies on the tunnel's *owning* node, whose `wireguard_peers` rows live in
that daemon's own store, while the `clusters` registry lives in the daemon the operator
attached from — so a write-time hook would have to cross a node boundary to do its job,
and would fail silently when it couldn't. Read-side derivation has no such coupling and
adds no new write path.

## Notes for the next agent

- **The linkage inherits `wireGuardReadService`'s locality caveat**, which predates this
  change: both `federationTunnelAdapter` and the new `TunnelIDForCluster` read *this
  daemon's own* store, so a cluster linked to a tunnel on a **peer** node won't resolve.
  That's the same documented "cluster-wide fan-out is a follow-up" gap `cmd/vnproxd/wireguard.go`
  already carries, not a regression — but if someone closes that gap, close it for both.
- **`wgTunnelSource` has no UI consumer yet** beyond the wizard's already-linked marker.
  The natural home is a federation-cluster settings screen, which still doesn't exist —
  the registry has full CRUD routes and no editor UI at all. That, not this linkage, is
  the bigger remaining federation UX gap.
- **Needs hardware validation:** the derived-link path was exercised only against
  `internal/pvemock` fixtures and the store's own SQLite. The live half — a real
  `wg show` handshake going stale on a tunnel whose linkage is peer-derived rather than
  explicit — has not run against real WireGuard.
