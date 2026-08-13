# PVE compatibility matrix

`docs/roadmap.md`'s Compatibility policy commits to "a compatibility validation task within one
phase of each new PVE release." This document, and the mechanism behind it, is that task (T-2103).

## Two matrices, not one — read this before the table below

vnprox has **two separate mechanisms** that both produce something that could be called a
"compatibility matrix," and this repository is deliberately strict about never letting them blur
into each other (Phase 18's mock-vs-hardware distinction, `planning/reports/needs-hardware-validation.md`):

- **This document (T-2103) is mock-validated.** It runs a small, representative set of real HTTP
  integration checks against `internal/pvemock`, once per PVE version this matrix tracks, through
  `internal/pvemock.NewCompatServer` — a wrapper that enforces the specific, documented API-shape
  differences between PVE releases this repository currently models (see "What is modeled" below).
  It runs on every commit, needs no cluster, and can therefore cover every version the compatibility
  policy promises support for. What it cannot do is tell you whether real PVE actually behaves the
  way this mock says it does.
- **`vnproxctl telemetry` (T-2503, `internal/telemetry`) is hardware-validated, but only where an
  operator has opted in and run `vnproxctl verify`.** It aggregates real `vnproxctl verify` results
  from real clusters in the field — see `docs/deployment.md`'s "`vnproxctl telemetry`" section and
  `docs/security.md`'s "Compatibility telemetry" section. This document does not read, aggregate, or
  reproduce any telemetry output; it has none to reproduce, since telemetry submission is opt-in and
  this repository does not operate a collection endpoint.

**Every cell in the table below carries `validation: mock` and nothing in this document should ever
be read as a claim that real Proxmox VE was involved.** The one genuine hardware data point this
repository currently has for any PVE version is a single-node deployment note in
`planning/reports/needs-hardware-validation.md` (pvecube, `pve-manager/9.2.4`, 2026-08-05) — narrow,
single-node, and gathered by a human reading real output, not by this matrix's automation. It is
**not** folded into the table below, on purpose: doing so would mix a hardware observation into a
mock-generated row, which is exactly the blurring this document exists to prevent.

## What is modeled

The mock-validated mechanism does not attempt to re-derive every difference between PVE releases —
that would mean inventing facts about a product this repository has no hardware to observe
(`CLAUDE.md`'s "no live Proxmox cluster" note applies here as everywhere else). It currently models
exactly one documented, checkable divergence:

- **SDN Fabrics (PVE 9.0+).** PVE 9.0 added SDN "Fabric" zone types (OSPF/OpenFabric-managed
  underlay routing) to the SDN zone `type` enumeration — documented in Proxmox's 9.0 release notes
  and SDN documentation. PVE 8.2 has no such zone type. `internal/pvemock.PVEVersionProfile` encodes
  this as `SDNFabricZones`, and `internal/pvemock.NewCompatServer` rejects an `openfabric`/`ospf`
  zone create/update with a PVE-shaped 400 on any profile where it is false. This is the one case the
  matrix's `sdn_fabric_zone_gate` check exercises per cell, and the case
  `internal/apicontract/compat`'s and `internal/pvemock`'s own tests mutation-prove (see those
  packages' doc comments, and this task's report, for the actual red/green run).

Everything else this matrix checks (ticket auth, a network read, an ordinary `vlan` zone create) is
checked because it is the minimum viable "does the mock even come up and answer for this version"
smoke test, not because it is known to vary by version. A fixture-shape divergence this repository
later learns matters (from a real capture — see below — or from a Proxmox changelog) is added the
same way: a new `PVEVersionProfile` field, a new gate in the compat server wrapper, a new check.

**The real-hardware-capture pathway already exists and is a separate mechanism from this one:**
`make record` (`internal/pvemock/testdata/cassettes/`, T-2502) records verbatim request/response
pairs from a live PVE API, one directory per PVE release, replayed byte-for-byte by
`pvemock.ReplayServer`. No real-PVE-recorded cassette exists in this repository yet — see that
directory's README for what would need to change once one does. This matrix's per-version YAML
fixtures (`testdata/clusters/compat/`) are a different, complementary thing: small, hand-written
topologies paired with a profile, built to exercise the compat server wrapper cheaply, not a
hardware capture.

## How it runs

- `internal/apicontract/compat` (`matrix.go`, `checks.go`) drives the checks and produces a
  `Matrix` — one `CellResult` per `(fixture, PVEVersionProfile)` pair in `Cells`.
- `TestMatrix_MatchesPublishedArtifact` runs on every `go test ./...` (therefore in CI, via `make
  check` — T-2103 AC1): it regenerates the matrix from scratch each time (real HTTP checks against a
  real, in-process compat-wrapped mock server, not a cached fixture) and fails if the result no
  longer matches the committed `docs/compat-matrix.json`. The same run also writes the freshly
  generated result to `var/compat-matrix.json`, version-stamped from `VNPROX_COMPAT_VERSION` if set
  — the literal "machine-readable result per cell" artifact a CI run leaves behind.
- `make compat-matrix` regenerates both `docs/compat-matrix.json` and the table below (`-update`).
  `docs/compat-matrix.json`'s committed `vnprox_version` is the literal placeholder `"unversioned"`
  — the same convention `docs/openapi.json` already uses (`cmd/vnproxd/openapi_test.go`'s
  `openAPIVersionPlaceholder`) — so the file does not churn on every commit. The release workflow
  stamps the real release version into a separate, published copy at release time (AC4: regenerated
  on release, not hand-maintained) without touching this committed placeholder copy, mirroring
  T-2101's `docs/openapi.json`/`docs/automation-contract.json` release-stamping precedent exactly
  (`.github/workflows/release.yml`).

## The matrix

<!-- BEGIN T-2103 GENERATED MATRIX (source: internal/apicontract/compat, `make compat-matrix`) -->

vnprox version: `unversioned` — generated 2026-08-13T18:09:45Z

| PVE version | Validation | Result | Checks | Fixture |
|---|---|---|---|---|
| 8.2 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabric_zone_gate:ok | `testdata/clusters/compat/pve-8.2.yaml` |
| 9.0 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabric_zone_gate:ok | `testdata/clusters/compat/pve-9.0.yaml` |
| 9.2 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabric_zone_gate:ok | `testdata/clusters/compat/pve-9.2.yaml` |

<!-- END T-2103 GENERATED MATRIX -->

`validation: mock` on every row is not a placeholder — it is this document's whole point. A future
hardware-validated row, if one is ever added here, would need its own explicit column or table, never
a silent edit of the `mock` label on an existing one.

## Reading a cell

Each cell records every check's individual pass/fail, not just a single bit. `sdn_fabric_zone_gate`
failing on the 8.2 row would mean pvemock's version gate stopped enforcing the divergence it exists
to catch — see "What is modeled" above. `auth_ticket` or `network_read` failing on any row would mean
something broke in the mock server itself, unrelated to version-specific behavior.
