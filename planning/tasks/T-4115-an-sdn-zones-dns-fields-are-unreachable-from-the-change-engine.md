# T-4115 · An SDN zone's DNS fields are unreachable from the change engine

**Status:** filed by T-4114, 2026-08-30 · **size:** S · **depends:** T-4112 (done), T-4114 (done) ·
**affects:** `internal/change/params_sdn.go`, `validate_schema.go`, `validate_referential.go`,
`apply_sdn.go`, `internal/pvemock/sdn.go`, `docs/api.md`, `docs/data-model.md`

## The observation

T-4112 established where DNS domains come from: an SDN zone's own `dnszone` (forward domain), `dns`
(the PowerDNS connection serving it) and `reversedns` (a separate connection for PTRs, with **no**
fallback to `dns`). `internal/pve.SDNZone` carries all three and the SDN poll reads them.

`SdnZoneCreateParams` and `SdnZoneUpdateParams` carry none of them:

```go
type SdnZoneCreateParams struct {
    Type, Bridge, Controller, IPAM string
    Nodes, ExitNodes, Peers        []string
    VrfVxlan, MTU                  int
}
```

So vnprox can read which domain a zone registers, can read and write the records inside it
(T-4112), and can manage the PowerDNS connection it is served by (T-4114) — but cannot set or
change the field that makes the domain exist. DNS domains are, to the change engine, read-only.

Three consequences, in ascending order of how much they matter:

1. An operator who wants SDN DNS has to configure `dnszone` in the PVE UI, then use vnprox for
   everything downstream of it.
2. `sdn.zone.create` cannot express a zone that PVE would accept as DNS-enabled, so a changeset
   cannot stand up a working SDN DNS setup end to end.
3. **The SDN rollback cannot restore a domain.** If an applied changeset's `sdn.zone.update`
   cleared `dnszone`, the rollback has no op that puts it back. T-4114 stopped the restore from
   emitting the *wrong* op for this (it used to emit `sdn.dns.zone.create` against the domain name,
   which could not validate on four counts); it could not add the right one, because the right one
   does not exist.

## What this task is

Add `dnsZone`, `dns` and `reverseDns` to `SdnZoneCreateParams`/`SdnZoneUpdateParams`, validate
them, and reconcile them in `sdnRestoreOps`' zone half.

Details that are not obvious:

- **`dns` and `reverseDns` are references to `/cluster/sdn/dns` connection ids**, so they need the
  same referential treatment `controller` and `ipam` already get: a zone naming a connection that
  does not exist must be rejected at stage time, and a `sdn.dns.server.delete` whose connection is
  still named by a zone's `dns`/`reverseDns` must be blocked. The existing DNS deletion guard
  (T-4114) covers "the connection still serves records"; this is the sibling case, "a zone still
  points at it", and it is the one `checkSdnControllerDeletable` is the model for.
- **`reversedns` has no fallback to `dns`.** This is read off `PowerdnsPlugin.pm`, not inferred: a
  zone with `dnszone` and `dns` set but no `reversedns` writes forward records and no PTRs at all.
  Anything that treats an empty `reversedns` as "same as dns" will produce PTRs PVE never writes.
- **`dnszone` is a domain name**, so it takes `validDNSName` — the opposite charset from the
  connection id beside it, which takes `validSDNObjectID`. Getting these two the wrong way round is
  the exact mistake T-4112 found and T-4114 finished cleaning up; a test should pin both.
- Verify the field names and their optionality against pvecube before modelling
  (`pvesh usage /cluster/sdn/zones create -v` / `set -v`), per CLAUDE.md, and check the transcript
  into `planning/reports/evidence/`. Do not take the three names from this card.

## Acceptance criteria

1. `sdn.zone.create`/`.update` accept `dnsZone`/`dns`/`reverseDns`, validated with the right
   charset for each, and apply them to PVE.
2. A zone naming a non-existent PowerDNS connection is rejected at stage time, and a test proves
   it.
3. `sdn.dns.server.delete` is blocked while a zone's `dns` or `reverseDns` still names the
   connection, with a finding that says which zone.
4. `sdnRestoreOps` restores a cleared or changed `dnszone`, and a test asserts the round trip —
   this is the consequence that made the card worth filing.
5. The PVE field names are confirmed against pvecube with the transcript checked in, not taken
   from this card or from `internal/pvemock`.
