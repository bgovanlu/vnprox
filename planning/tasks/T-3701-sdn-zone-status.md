# T-3701 · SDN zone status: call the endpoint PVE actually has

**kind:** defect (correctness, SDN) · **found by:** full audit, 2026-08-21 · **size:** M ·
**severity:** high — the feature has never worked against real PVE ·
**context:** `planning/reports/evidence/pve-9.2.4-sdn-zone-status.txt` (the read-only transcript this
card is built from), `internal/pve/sdn.go`, `internal/collect/pve.go`, `internal/pvemock/sdn.go`,
CLAUDE.md ("Never model a PVE object from `internal/pvemock/`…")

## The defect

`internal/pve/sdn.go:183` builds `GET /cluster/sdn/zones/{zone}/status`. **That endpoint does not
exist on PVE 9.2.4.** The node returns `501 Method not implemented`, `internal/collect/pve.go:386`
logs a warning and skips, and the deployed instance has done this **8,540 times in the last 24
hours** — roughly six per minute, indefinitely.

The endpoint that does exist is per-**node**, not per-zone:

```
$ pvesh usage /nodes/pvecube/sdn/zones -v
USAGE: pvesh get /nodes/pvecube/sdn/zones
  Get status for all zones.

$ pvesh get /nodes/pvecube/sdn/zones --output-format json
[{"status":"error","zone":"labz"}]
```

## Why it survived every test

`internal/pvemock/sdn.go:159` **serves the route vnprox invented**, and the comment beside it
(`sdn.go:278`) explains the shape with the words *"Real PVE surfaces per-node…"*. The client was
built to match the mock; the mock was written from the same secondary source as the client. They
agree with each other and neither agrees with the node.

This is the same shape as Phase 31's SDN Fabrics finding, and the reason CLAUDE.md's rule exists. It
is worth stating once more because this instance is the sharpest yet: **the defect is invisible to
the entire test suite by construction.** No amount of test-writing against `pvemock` could have
found it. What found it was reading the deployed daemon's own logs.

## Product impact, not just log noise

`labz` is a `simple` zone on `pvecube` and its realization status is `error`. `GET /api/v1/findings`
on the deployed instance returns three findings and none of them is that one. A product whose
purpose is surfacing network problems cannot see a broken SDN zone on the only node it runs on.

## Deliverables

1. `internal/pve`: replace `GetSDNZoneStatus(ctx, zone)` with a per-node call against
   `/nodes/{node}/sdn/zones` returning every zone's status for that node. Note this is an
   **inversion of the call's axis**, not a URL substitution — and it is strictly cheaper (N nodes,
   not N zones).
2. `internal/collect/pve.go`: iterate nodes, not zones; stop treating one failure as per-zone.
3. `internal/pvemock`: **delete** the `/cluster/sdn/zones/{zone}/status` route and serve
   `/nodes/{node}/sdn/zones`. Leaving both would let the wrong shape keep passing — that is the
   whole lesson here.
4. Raise a finding when a zone reports `status != "ok"`, so a broken zone is visible in the product
   rather than only in a log. Detection-only (`Fixable: false`): PVE's zone status does not tell us
   *why*, and inventing a remedy from a one-word status would repeat this card's own mistake.

## Acceptance criteria

1. No `501` for SDN status in the deployed daemon's log after upgrade — verified on `pvecube`, not
   asserted from the mock.
2. `pvemock` no longer serves any route PVE 9.2.4 does not; a test asserts the per-node shape.
3. `labz`'s `error` status is visible via `GET /api/v1/findings` on the deployed instance.
4. `make check` green; e2e green.

## Explicitly checked, so the fix is not over-generalised

`/nodes/{node}/sdn/vnets` and `/nodes/{node}/sdn/fabrics` appear in `pvesh ls` but are not the same
kind of endpoint — `pvesh get /nodes/pvecube/sdn/vnets` returns *"No 'get' handler defined"*. `zones`
is the only per-node SDN status collection on 9.2.4.

## Needs hardware validation

One node, one zone, one status value (`error`). The per-node shape exists precisely because
different nodes can report different status for the same zone, and that is unobservable here.
