# T-4114 · `sdn.dns.zone.*` manages a PowerDNS server connection, and its name says otherwise

**Status:** DONE, 2026-08-30 · **size:** S (delivered larger — see "What was built") · **depends:** T-4112 (done) ·
**affects:** `internal/change/op.go`, `params_sdn_dns.go`, `validate_*`, `docs/api.md`,
`contrib/terraform-provider-vnprox`, `contrib/ansible`, `web/src/changesets`

## The observation

T-4112 established that `/cluster/sdn/dns` is a flat list of **PowerDNS server connections** — url,
key, ttl, fingerprint — and not a list of DNS zones. The DNS domains live on the SDN zone
(`dnszone`/`dns`/`reversedns`).

The three ops that manage those connections are still called:

```
sdn.dns.zone.create
sdn.dns.zone.update
sdn.dns.zone.delete
```

and their target kind is `sdn-dns-zone`. Neither names a zone. An operator reading a changeset
preview that says *"create DNS zone `pdns1`"* is being told something false about what will happen,
and the id charset now enforced (`[a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]`, no dots) makes the mismatch
visible in the worst way: the op named "zone" rejects every domain name.

T-4112 corrected the params, the validators, the apply path and the docs, and deliberately did
**not** rename the ops. The reason is in CLAUDE.md: *"API routes, JSON field names, and error
format: follow `docs/api.md` exactly — other tasks depend on those contracts"*, and *"do not
re-litigate decisions … flag it in your report instead of changing it unilaterally"*. An op type
string is exactly such a contract — the Terraform provider, the Ansible modules and any changeset
JSON an operator has saved all carry it.

## What this task is

Rename the family to `sdn.dns.server.*` with the target kind `sdn-dns-server`, **with a migration
path**, not a flag day.

- Accept both op type strings on the way in for one release; emit only the new one.
- Same for the target kind, including in `internal/inventory`'s `KindSDNDnsZone`.
- Update `contrib/terraform-provider-vnprox` and `contrib/ansible` together with it — the whole
  reason the rename was deferred is that they are downstream of it.
- The `inventory.SdnDnsZone` entity keeps its name and meaning: after T-4112 it genuinely IS a DNS
  domain (derived from an SDN zone's `dnszone` plus the reverse zones from its subnets). Only the
  **op family** and its target are misnamed. This distinction is the easy thing to get wrong here.

## Acceptance criteria

1. `sdn.dns.server.create/update/delete` exist, are what the engine emits, and manage
   `/cluster/sdn/dns` instances.
2. A changeset carrying the old `sdn.dns.zone.*` strings still validates and applies, and a test
   proves it — an operator's saved changeset must not become unreplayable.
3. A preview line for one of these ops names a *server connection*, not a zone. A test asserts the
   wording, because "the preview said zone" is the defect being fixed.
4. `docs/api.md` documents both names and which is deprecated; the Terraform and Ansible
   integrations emit the new one.


## What was built

The rename, with a migration, plus three defects the rename made undeniable. All four are the same
root cause wearing different hats: T-4112 changed what this op family *means* without changing what
its consumers assume its target *is*.

**1. The rename (criteria 1, 2, 4).** `sdn.dns.server.create/update/delete`, target kind
`sdn-dns-server`. `internal/change/op.go` gained `deprecatedOpTypes` and `OpType.Canonical`; the
decoder canonicalizes the op string before the params factory is looked up, so no switch anywhere
else needed a second case. The target kind is rewritten too, but **only for these three op types** —
`sdn-dns-zone` is still a live kind naming a DNS domain, and a record op that names one must keep
meaning a domain. `internal/change/op_migration_test.go` decodes a verbatim pre-rename changeset and
asserts it decodes, validates, re-encodes canonically, and that the kind rewrite does not leak to
record ops.

**2. The preview said "zone" (criterion 3).** `internal/explain`'s noun for this family was "an SDN
DNS zone", so the operator-facing line read `Deletes an SDN DNS zone "pdns1".` — describing the
destruction of a domain's records when a server connection is what would be removed. Now "a
PowerDNS server connection". The retired op string stays mapped, with the *corrected* noun, because
historical audit records were written before the rename and never pass through the canonicalizing
decoder. Verified non-vacuous by reverting the noun and watching the test fail with that exact
sentence.

**3. The rollback emitted ops that could not validate.** `sdnDnsRestoreOps` reconciled the snapshot's
DNS **domains** using what are now the server ops, producing `sdn.dns.zone.create` with a dotted
domain as its target id and none of the type/url/key PVE requires — four schema violations in one
op, on the rollback path. Confirmed empirically before touching it, by running the produced op back
through `schemaValidate`. It now reconciles a new `SDNConfig.DnsServers` (the actual
`/cluster/sdn/dns` set, captured in `changeagent.go`) and emits **no ops for domains at all**, since
a domain has no API to create it. A test asserts every emitted restore op passes the same schema
validation a staged op does — the assertion whose absence let this live.

Recreating a deleted connection is knowable but not executable: PVE never returns the API key on a
read, so no snapshot can hold one. Rather than emit an op guaranteed to 400 or skip the object in
silence, `sdnRestoreOp` gained a `blocked` reason that `restoreSDN` reports as a rollback failure
naming the connection and what is missing.

**4. `DnsUnreadable` was written and never read.** T-4112 added the field with an explicit rationale
— "a rollback that silently restores 'no records' for a domain whose PowerDNS server happened to be
unreachable at capture time would delete records nobody asked it to" — and nothing consumed it. The
restore now skips records in any domain **either** side could not read: absence on the pre side means
"not captured", absence on the live side means "not observed", and both are disqualifying.

**5. The DNS deletion guard could never fire.** `dnsZoneDeletionGuardFindings` keyed its op index by
`op.Target.ID` (a connection id) and looked it up by `rec.Zone` (a domain). After T-4112 those are
different namespaces, so the lookup missed on every input: T-1204 acceptance criterion 3 was a
passing check measuring nothing, and no test caught it because the fixtures used one string for
both. The missing hop is `SdnDnsZone.DNS` — connection → its domains → their records. The finding
now labels each blocking record with its domain, since one connection can serve several.

## Deviations

- **Criterion 4's contrib clause is vacuous, not done.** `contrib/terraform-provider-vnprox` and
  `contrib/ansible-collection-vnprox` cover `bridge` and `vlan` only; neither mentions any
  `sdn.dns.*` op. There was nothing to update. The card assumed otherwise, and that assumption was
  the stated reason the rename was deferred from T-4112 — so the deferral was more cautious than it
  needed to be.
- **Delivered larger than size S.** Items 3-5 are not renames; they are correctness fixes on the
  rollback and validation paths. They are here rather than filed because the rename is what makes
  them visible, and shipping a rename that leaves the restore emitting domain-targeted ops would
  have made the wrong behaviour *look* correct.

## Filed

**T-4115** — an SDN zone's `dnszone`/`dns`/`reversedns` are unreachable from the change engine, so a
DNS domain is read-only and a rollback cannot restore one that a changeset cleared. This is the gap
item 3 above ran into: it could stop the restore emitting the wrong op, but the right op does not
exist yet.
