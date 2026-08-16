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
that would mean inventing facts about a product this repository could not observe. It currently
models exactly one checkable divergence:

- **SDN Fabrics (PVE 9.0+).** PVE 9.0 added SDN "Fabrics" — underlay routing, reachable at its own
  API family, `/cluster/sdn/fabrics`, with `fabric` and `node` sub-collections and an `all` read.
  PVE 8.2 does not serve that path. `internal/pvemock.PVEVersionProfile` encodes this as
  `SDNFabrics`, and `internal/pvemock.NewCompatServer` answers a PVE-shaped **501** for any request
  at or below that path on a profile where it is false, and the shape hardware returns
  (`{"fabrics":[],"nodes":[]}`) where it is true. This is the one case the matrix's
  `sdn_fabrics_api_gate` check exercises per cell, and the case `internal/apicontract/compat`'s and
  `internal/pvemock`'s own tests mutation-prove.

> **This entry was wrong from T-2103 until 2026-08-16, and its check passed the entire time.**
> The previous model asserted that PVE 9 added `openfabric`/`ospf` to the SDN *zone type*
> enumeration, and the matrix published a green `sdn_fabric_zone_gate` cell for every release on
> every commit. It was written from Proxmox's 9.0 release notes, which describe the feature but not
> the surface it landed on. The first PVE 9 node this project ever had access to (pvecube, 9.2.4)
> reports a zone type enum of `<evpn | faucet | qinq | simple | vlan | vxlan>` — `openfabric` and
> `ospf` are not zone types at all, so real 8.2 and real 9.2 *both* reject an `openfabric` zone and
> the gate tested a difference that does not exist in either direction. Fabric protocols
> (`bgp | openfabric | ospf | wireguard`) are a field on a fabric object, a different namespace.
> The capture is checked in at
> [`planning/reports/evidence/pve-9.2.4-sdn-schema.txt`](../planning/reports/evidence/pve-9.2.4-sdn-schema.txt).
>
> The general lesson, which applies to every future entry in this list: **a compatibility check
> derived from release notes tests the release notes.** A mock and a check written from the same
> secondary source will agree with each other indefinitely. Every entry added here from now on
> states the surface its expectation was read off, and prefers a capture to a changelog.

Two facts that same capture turned up, recorded here because they are compatibility facts even
though neither is version-gated, and both are carded rather than fixed in passing:

- `faucet` is a real SDN **zone** type on PVE 9.2 and is absent from `internal/change`'s
  `validSdnZoneTypes` — vnprox refuses to stage a zone real PVE would accept.
- `faucet` is likewise a real SDN **controller** type (`bgp | evpn | faucet | isis`), and
  `/cluster/sdn` carries two further families vnprox does not model at all: `prefix-lists` and
  `route-maps`.

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
- `TestMatrix_MatchesPublishedArtifact` runs on every `go test ./...` (therefore in the gate, via
  `make check` — T-2103 AC1; that gate runs on the dev host via `scripts/ci-local.sh`, not on
  GitHub Actions, which has been disabled since 2026-08-13): it regenerates the matrix from scratch each time (real HTTP checks against a
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

vnprox version: `unversioned` — generated 2026-08-16T23:09:47Z

| PVE version | Validation | Result | Checks | Fixture |
|---|---|---|---|---|
| 8.2 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabrics_api_gate:ok | `testdata/clusters/compat/pve-8.2.yaml` |
| 9.0 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabrics_api_gate:ok | `testdata/clusters/compat/pve-9.0.yaml` |
| 9.2 | mock | pass | auth_ticket:ok, network_read:ok, sdn_zone_baseline:ok, sdn_fabrics_api_gate:ok | `testdata/clusters/compat/pve-9.2.yaml` |

<!-- END T-2103 GENERATED MATRIX -->

`validation: mock` on every row is not a placeholder — it is this document's whole point. A future
hardware-validated row, if one is ever added here, would need its own explicit column or table, never
a silent edit of the `mock` label on an existing one.

## Reading a cell

Each cell records every check's individual pass/fail, not just a single bit. `sdn_fabrics_api_gate`
failing on the 8.2 row would mean pvemock's version gate stopped enforcing the divergence it exists
to catch — see "What is modeled" above. Note that on the 8.2 row this check passes by being
*refused*: the tests assert each cell's detail string, not just its boolean, because a check whose
name survives a rewrite while its meaning changes underneath is precisely how the previous gate
stayed green for four phases while describing a PVE that does not exist. `auth_ticket` or `network_read` failing on any row would mean
something broke in the mock server itself, unrelated to version-specific behavior.
