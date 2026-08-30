# T-4114 · `sdn.dns.zone.*` manages a PowerDNS server connection, and its name says otherwise

**Status:** filed by T-4112, 2026-08-30 · **size:** S · **depends:** T-4112 (done) ·
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
