# T-4112 · `internal/pve`'s SDN DNS record endpoints are modelled against a PVE surface that does not exist

**Status:** DONE, 2026-08-30 — option B (talk to PowerDNS) chosen and built. See "What was built" at the end.
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

---

## Verified on pvecube, and the scope is larger than this card recorded

Evidence: `planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt`, captured 2026-08-30 against
pvecube (PVE 9.2.4), read-only `pvesh usage`/`ls` only.

The card's central claim holds exactly:

```
$ pvesh usage /cluster/sdn/dns/{dns}
USAGE: pvesh get /cluster/sdn/dns/{dns}
USAGE: pvesh set /cluster/sdn/dns/{dns}  [OPTIONS]
USAGE: pvesh delete /cluster/sdn/dns/{dns}  [OPTIONS]

$ pvesh usage /cluster/sdn/dns/{dns}/records
no such resource '/cluster/sdn/dns/{dns}/records'

$ pvesh usage /cluster/sdn/dns/{dns}/resolve
no such resource '/cluster/sdn/dns/{dns}/resolve'
```

`{dns}` supports get/set/delete and nothing else. Both invented sub-paths are `no such resource`.

### It is five functions, not two

This card names `ListSDNDnsRecords` and `ResolveSDNDnsRecords`. `internal/pve/sdn_dns.go` has
three more on the same invented URL space:

| function | route | exists on PVE 9.2.4 |
|---|---|---|
| `ListSDNDnsRecords` | `GET /cluster/sdn/dns/{zone}/records` | **no** |
| `ResolveSDNDnsRecords` | `GET /cluster/sdn/dns/{zone}/resolve` | **no** |
| `CreateSDNDnsRecord` | `POST /cluster/sdn/dns/{zone}/records` | **no** |
| `UpdateSDNDnsRecord` | `PUT /cluster/sdn/dns/{zone}/records/{name}/{type}` | **no** |
| `DeleteSDNDnsRecord` | `DELETE /cluster/sdn/dns/{zone}/records/{name}/{type}` | **no** |

The three write functions are wired into `cmd/vnproxd`'s `pveGateway.SDNStageOp` — the change
engine's staging dispatcher — behind the `sdn.dns.record.create` / `.update` / `.delete` op types
(`internal/change/op.go:71-73`), which are schema-validated and appear in `apply_plan.go`'s tables.
**Applying a changeset containing one of them would POST to a route that does not exist.**

### And "no visible failure" is not true

> *"Nothing in the shipped product calls these two functions against a real node on a normal path."*

`internal/collect/pve.go:420` calls `ListSDNDnsRecords` **once per DNS zone on every collector poll
cycle**. On any real cluster with a PowerDNS plugin configured, that is a 404 every cycle, forever.

The consequence is not silent. The poll's error branch `continue`s, so no `SdnDnsRecord` entity
ever enters inventory — and `docs/features/monitoring.md` §59 documents exactly what the PTR
audit does with that state: **`ptr_zone_unreadable`**, raised because "this state is genuinely
indistinguishable from *the zone has no records yet*". So T-4109's PTR completeness audit reports
every reverse zone as unreadable, permanently, on every real cluster — and reports it correctly,
given what it is told.

Nothing failed in CI because `internal/pvemock` serves all four invented routes
(`internal/pvemock/sdn_dns.go:45-48`). This is CLAUDE.md's warning realised precisely: *"A mock and
the check that tests it, both derived from the same secondary source, will pass together forever."*

### Why this is not mine to decide

This card authorises deleting the two READ functions. The larger surface it did not know about
cannot be deleted the same way, because deleting it removes a shipped feature's data source:

- **Option A — delete record-level support.** Remove five client methods, three change-engine op
  types and their validation, and four mock routes. **T-4109's PTR coverage audit goes with it** —
  it exists to compare forward and reverse records, and there would be no records. That is
  removing a documented feature (`docs/features/monitoring.md` §59), not dead code.
- **Option B — implement against the real surface.** PVE writes records straight into the backing
  PowerDNS server from the plugin; there is no PVE API for them. vnprox would talk to PowerDNS
  directly with the operator's configured `url`/`key`/`fingerprint` (all three are real fields on
  `/cluster/sdn/dns`, per the transcript). That keeps the PTR audit working and is a new outbound
  integration with its own auth, TLS-pinning and failure modes.

**Recommendation: B**, but not silently — it is a new external dependency. A is cheaper and honest
about today's reality; it also deletes a feature that was shipped and documented on the strength of
routes nobody checked, which is a product call rather than a cleanup.

What is done regardless of the choice: the evidence transcript exists, and the doc comment's
"unconfirmed shapes" wording is now known to understate "these endpoints do not exist".

---

## What was built (option B, 2026-08-30)

The choice was between deleting record-level support and building the outbound PowerDNS
integration. Option B: PVE writes records straight into PowerDNS, so vnprox reads them from the
same place, with the same url/key/fingerprint the operator already configured.

**Everything below the API line was read off pvecube, not inferred.** `PowerdnsPlugin.pm`,
`PVE::Network::SDN::api_request`, `PVE::API2::Network::SDN::Dns`, and the reverse-zone naming
cross-checked against the same `Net::IP`/`NetAddr::IP` the plugin uses. Transcript appended to
`planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt`.

### New

- **`internal/powerdns`** — the client. `GET /zones/{zone}`, `?rrsets=false`, `PATCH` with
  `{rrsets:[...]}`, and the bare-base `Ping` the plugin uses as its own health check. Leaf
  certificate pinning that reproduces PVE's exactly (hostname verification off, exact SHA-256 at
  depth 0). `rrset.go` carries the three read-modify-write builders the plugin has, with the same
  early returns, so a record PVE wrote and a record vnprox wrote are indistinguishable afterwards.
  `reverse.go` ports `get_reversedns_zone` — RFC1918 special cases and PVE's public-IPv4 mask quirk
  included, deliberately, with a test that says so.
- **`internal/sdndns`** — the join. `DeriveZones` works out every readable DNS domain from SDN
  configuration (an SDN zone's `dnszone`/`dns`/`reversedns`, plus a reverse zone per subnet CIDR),
  and reports every domain it declined to derive with a reason.

### Corrected

- `internal/pve/sdn_dns.go`: five invented methods deleted. `SDNDnsZone` → `SDNDnsPlugin`, with
  `ID` decoding from `dns` rather than `zone` — a one-word tag that made the id decode **empty**
  against every real cluster, and that no test caught because pvemock emitted `zone` too.
- `pve.SDNZone` gained `dns`/`dnszone`/`reversedns`, which is where the domains actually live.
- `internal/collect`: the poll that 404'd once per zone per cycle now reads PowerDNS. **This is the
  fix T-4109's PTR audit was waiting for** — it had been reporting `ptr_zone_unreadable` for every
  reverse zone, permanently, and correctly given what it was told.
- `cmd/vnproxd`: the three record ops apply against PowerDNS. Applying one of them used to POST to a
  route that does not exist — the failure landed on the one path where a wrong guess costs the most.
- `internal/pvemock`: the four invented routes are **deleted**, not supplemented, and the create
  handler now enforces the parameters PVE enforces.

### Two things the card did not know about, found on the way

1. **`sdn.dns.zone.create` could never have worked either.** Its params carried `{dns, ttl}`; PVE
   requires `dns, type, url, key`. Every applied create was a parameter-verification 400. The params
   are extended (additively) and the schema validator now requires them, so the failure moves from
   apply time to stage time. The op *names* are left alone — they are a wire contract the Terraform
   provider and Ansible modules depend on, and CLAUDE.md is explicit that those are not renamed
   unilaterally. **Filed: T-4114**, to rename them to `sdn.dns.server.*`.
2. **`GET /sdn/dns`'s `resolved` array modelled a duality that does not exist.** It was built as the
   DNS counterpart of `/sdn/dhcp`'s Reservation(config)/Lease(observed) split, but PVE keeps no
   record copy — there is one source. It is now always empty, documented as such, and kept on the
   wire rather than removed mid-flight. A test asserts the emptiness, because the failure mode here
   is someone later filling it with a copy of `records` and re-asserting a cross-check that never
   happened.

### What is still unproven, and why it is smaller than it was

`needs-hardware-validation.md`'s T-1204 section claimed the PVE-side wire shape needed a real node
with a DNS plugin configured. Confirming the *shape* needed no such thing — and reading it found
that the whole URL space was invented. The section is rewritten: what remains needing hardware is
**PowerDNS error-body shapes** and **zone notify/transfer semantics**, and both need a PowerDNS
server rather than PVE hardware, which a container would settle.
