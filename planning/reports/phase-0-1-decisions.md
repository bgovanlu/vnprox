# Phase 0/1 — decisions of record

**Date:** 2026-07-10 · **Context:** the phase 0/1 audits (audit-phase-0.md, audit-phase-1.md,
F-21) found that implementation decisions for those phases lived only in scattered code comments,
with no committed completion reports. This file is the consolidated record. Each entry names the
authoritative in-code location; if this file and the code comment ever disagree, fix whichever is
wrong and say so here.

## Documented-but-underspecified decisions (interpretations of the docs)

| # | Decision | Where | Authoritative comment |
|---|---|---|---|
| D-01 | TLS cert "file-watch" is 30s mtime polling, not inotify — no extra dep, SIGHUP covers immediate reload | T-002 | `internal/config/tls.go` (top comment) |
| D-02 | `Ref` wire encoding is `kind:node:id` with only the first two `:` structural; cluster-scoped refs have an empty node segment | T-103 | `internal/inventory/ref.go` |
| D-03 | Topology `layer` string vocabulary, `Topology.Layers` meaning, and the guest-collapse synthetic node id scheme | T-106 | `internal/topology/doc.go` + per-file comments it points to |
| D-04 | "Realm list from PVE" read as "forward caller-supplied realm verbatim"; docs/api.md defines no realms endpoint. Revisit if the UI needs a realm dropdown | T-105 | `internal/auth/doc.go` (§ GET /access/domains) |
| D-05 | Guest lists derive from `GET /cluster/resources` instead of per-node qemu/lxc list endpoints (same data, one call, and pvemock's surface matches) | T-101 | `internal/pve/guest.go` (top comment) |
| D-06 | Boolean inventory fields that a source may simply not report (`vlanAware`, `stp`, `linkUp`) use explicit `…Set` companions; unset = "not reported" → no merge win, no conflict, rendered as unknown | T-103 (audit F-14) | `internal/inventory/merge.go` (ownership doc comment) |
| D-07 | Per-source raw source retention: `host-interfaces` = verbatim stanza text; `pve-*` = pretty-printed JSON of the PVE object; `host-netlink`/`host-lldp` = compact JSON of observed state | T-103/T-106 (audit F-08) | `internal/inventory` (RawSource) + docs/api.md `GET /inventory/{ref}` |
| D-08 | Topology staleness rule: a collector source is stale after 3 consecutive poll failures; `Project` stays pure and the API handler decorates | T-106 (audit F-18) | `internal/api/topology.go` + docs/api.md |
| D-09 | Delta batches may double-attribute changes made by a concurrent loop inside a diff window; acceptable because `topology.delta` consumers treat deltas as idempotent re-read hints | T-104 (audit F-13) | `internal/collect/loop.go`, `refresh.go` |
| D-10 | Committed-changeset manual rollback is modeled as a *new* restoring changeset, not a status transition of the original | T-201 | `internal/change/changeset.go`; planning/reports/T-201.md |

## Acknowledged deviations from task cards (kept, with rationale)

| # | Deviation | Where | Rationale record |
|---|---|---|---|
| V-01 | Ethtool via SIOCETHTOOL ioctl + sysfs fallback, NOT the card's "netlink ethtool preferred, exec fallback". **Not pre-authorized by the card** — a prior comment falsely implied it was; corrected 2026-07-10 | T-102 | `internal/host/ethtool.go` (DEVIATION comment) |
| V-02 | pvemock's `GET /access/permissions` reports the fixture user's flat privilege list at path `/` rather than a per-path ACL tree | T-004/T-105 | `internal/pvemock` handler comment + README; needs-hardware-validation.md |
| V-03 | pvemock TOTP is a single-step `otp`-param check, not PVE's two-step NeedTFA ticket-challenge flow | T-004/T-105 | `internal/auth/doc.go`; needs-hardware-validation.md |
| V-04 | pvemock API tokens carry the owning user's full privileges (no privilege-separation modeling) | T-004 | `internal/pvemock` TokenSpec comment |
| V-05 | Host-side inventory sources are only retired for the local node; departed-peer retirement covers the four PVE sources (host/lldp data never exists for peers) | T-104 (audit F-01) | `internal/collect/pve.go` (retireDepartedNodes) |

## Comment-hygiene rule (adopted after the audit)

Three audit findings (phase-1 F-01, F-04, F-19) were cases of code comments claiming behavior or
task-card authorization that didn't exist. Standing rule for all future tasks: **a comment may not
claim a task card says something without quoting wording that actually appears in
`planning/tasks/`, and may not describe behavior the code doesn't implement.** Reviewers should
spot-check any "the card sanctions/says…" comment against the card.

## Related records

- Hardware-dependent behaviors that only a real PVE cluster can confirm: see
  `planning/reports/needs-hardware-validation.md`.
- Remediation outcomes for every audit finding: appendices in `audit-phase-0.md` /
  `audit-phase-1.md` and `planning/reports/audit-remediation.md`.
