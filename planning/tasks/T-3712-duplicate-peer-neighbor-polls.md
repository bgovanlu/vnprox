# T-3712 · Two subsystems poll the same peer endpoint, and the replay guard refuses the second

**Found by:** T-3711's deploy verification, 2026-08-25, while checking whether a freshly deployed
build had introduced anything. · **size:** M · **depends:** — · **affects:** T-1401 (neighbor
discovery), IPAM observation, rogue-DHCP detection, and every peer read that has more than one caller

## What was observed

`planning/reports/evidence/peer-neighbors-duplicate-poll-2026-08-25.txt`. On pvecube, read-only,
pve001 never logged into:

```
20205  "path":"/api/peer/host/neighbors"      <- rejected as replays, 7 days
   34  "path":"/api/peer/host/dhcp-leases"
~2885 rejections per day, every day since 2026-08-18. Exactly 2 per 30s.

09:25:20.591  GET /api/peer/host/neighbors  status 200  bytes 4442   from 192.168.1.7
09:25:20.615  GET /api/peer/host/neighbors  status 401  bytes   85   from 192.168.1.7
                                                       ^ 24ms later, identical request

over 30 minutes:  60 x status 200,  60 x status 401   — exactly half
```

## What it is, and what it is not

**It is not a security failure.** The replay guard is doing precisely its job: two byte-identical
signed requests arrived inside the cache TTL and the second was refused. T-3702/T-3703's work is
behaving correctly.

**It is not data loss for the cluster as a whole.** The first request returns 4442 bytes of real
neighbor data.

**It is a bug in the client.** Nothing dedupes peer reads. `internal/neighbor`'s `peerNeighborReader`
turns every call straight into an HTTP request — there is no cache and no singleflight in
`internal/neighbor/service.go`. `Neighbors` has several independent consumers:

- `internal/neighbor/service.go:125` (the fan-out itself)
- `internal/ipam/service.go:223`
- `cmd/vnproxd/rogue.go:54`

Two of them poll on the same cadence, so two identical requests leave for the same peer milliseconds
apart. **Whichever loses the race gets a 401 for data its sibling just fetched successfully** — so
one subsystem's view of remote-node neighbors is empty on every single poll, forever, and has been
for at least a week. Which subsystem loses is a race, which is worse than a deterministic failure.

## Why it went unnoticed for a week

It is logged as `peer: rejected unauthorized request` at WARN — wording that reads as *someone is
attacking us*, not *we are calling ourselves twice*. Anybody skimming the journal classifies it as
security noise from a peer and moves on. That is what happened; it took a line-by-line read of a
post-deploy diff to catch it.

Add this to the list with the sFlow `frame_length` bug and the conntrack procfs assumption: not a
fixture agreeing with itself this time, but the same family — **a message whose wording made the
reader draw the wrong conclusion, repeated 20,000 times without anyone re-deriving it.**

## Deliverables

- **Dedupe peer reads.** A short-TTL cache or `singleflight` in front of the peer client so N
  consumers of the same peer read within one poll window produce one request. Check whether
  `internal/dhcp`'s `PeerSource` (the same seam shape) has the same problem — the 34 `dhcp-leases`
  rejections suggest it does, at a lower rate.
- **Fix the log wording.** A replayed request from a *known, authenticated* peer is a different event
  from a replayed request from an unknown source. Distinguish them, so the first cannot hide inside
  the second. This is at least as important as the dedupe.
- **A test.** Two concurrent consumers of the same peer read must produce one HTTP request. This is a
  property of our own code and can be tested honestly with a counting stub.

## Acceptance criteria

1. On `vnprox-dev`, `replayed peer request` for `/api/peer/host/neighbors` drops to zero over an hour,
   while neighbor data still arrives — both observed on the node, not inferred.
2. Every consumer of a remote node's neighbors gets data. Demonstrate the currently-losing subsystem
   getting a populated result.
3. A known-peer replay and an unknown-source replay are distinguishable in the journal.

## Note

The duplicate requests come from **pve001**, which runs an older vnproxd we cannot upgrade. So fixing
the client here does not stop the rejections we are currently observing — it stops *our* side doing
it to *them*, and to any future peer. Verify the fix by watching pvecube→pve001 direction from
pvecube's own client metrics, not by expecting pve001's behaviour to change. Do not treat continued
inbound rejections from pve001 as the fix having failed.
