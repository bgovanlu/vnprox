# T-4112 · `internal/pve`'s SDN DNS record endpoints are modelled against a PVE surface that does not exist

**Found by:** T-4109's PTR-coverage work, 2026-08-28 · **size:** M ·
**depends:** — · **affects:** `internal/pve/sdn_dns.go`, `internal/pvemock`'s DNS fixtures, any
feature that reads or writes SDN DNS records

## The observation

`internal/pve/sdn_dns.go` currently carries this disclaimer:

> The exact real-PVE/PowerDNS wire shapes are unconfirmed against live hardware — see
> `planning/reports/needs-hardware-validation.md`.

**That is too soft, and the node says so.** Verified on pvecube (PVE 9.2.4, read-only
`pvesh get`/`ls`/`usage` plus reading `PVE/Network/SDN/Dns/PowerdnsPlugin.pm` and `Subnets.pm`):

- `/cluster/sdn/dns` is a **flat list of PowerDNS plugin instances** — url, key, ttl — **not a list
  of DNS zones**.
- `pvesh ls /cluster/sdn/dns/<id>` reports it **"does not define child links"**. There is no
  `records` sub-resource and no `resolve` sub-resource on real PVE, at any level.
- PVE writes individual records **straight into the backing PowerDNS server**, per record, from
  the plugin. There is no PVE API surface that enumerates them.

So `ListSDNDnsRecords` (`GET /cluster/sdn/dns/{zone}/records`) and `ResolveSDNDnsRecords` target
routes that do not exist. The functions are not merely unvalidated — their URL space is invented.
`internal/pvemock` serves them, which is why nothing has ever failed.

This is the exact pattern `CLAUDE.md` warns about, and the third instance this project has found:
a mock and the code that calls it agreeing with each other and with nothing real. The SDN-fabric
zone-type error (Phase 31) and the `/cluster/sdn/zones/{zone}/status` route (T-3701) were the
first two.

## Why it has not caused a visible failure

Nothing in the shipped product calls these two functions against a real node on a normal path.
T-4109's PTR-coverage check deliberately reads `inventory.Snapshot` — config-truth already
ingested via `FromPVEDNS` — rather than calling them, so it is unaffected. The invented routes sit
in the client waiting for the first feature that trusts them.

## Deliverables

- **Decide what the real surface is**, from the node: does vnprox need record-level reads at all,
  and if so does it read them from PowerDNS directly (the plugin's own API, with the operator's
  configured url/key) rather than through PVE? Capture the answer as an evidence transcript in
  `planning/reports/evidence/`.
- Either implement against the real surface, or **delete `ListSDNDnsRecords`/`ResolveSDNDnsRecords`
  and the mock routes that serve them.** A client method for a route that does not exist is worse
  than a missing feature, because it reads as capability.
- `internal/pvemock` must stop serving the invented routes — deleted, not merely supplemented. That
  is the same correction T-3701 had to make.
- Correct the doc comment: "unconfirmed shapes" understates "these endpoints do not exist".
- Re-check `needs-hardware-validation.md`'s entry for this and restate it accurately.

## Acceptance criteria

1. No function in `internal/pve` targets a `/cluster/sdn/dns/**` sub-path that `pvesh ls` does not
   report, and a test or evidence transcript demonstrates the check was made.
2. `internal/pvemock` serves no route that real PVE 9.2.4 does not.
3. If record-level access survives in any form, an evidence transcript shows the real surface it
   talks to.
