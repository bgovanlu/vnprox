# ADR-0009: Target PVE 8.2+ and 9.x, with a forward target for each new major

**D-number:** D9 (`docs/architecture.md` §10)
**Status:** Accepted; the specific version range is revised each phase by design, not a fixed line

## Context

vnprox depends on several PVE-side capabilities that are only present from specific versions
onward: `ifupdown2` as the default network stack, SDN reaching general availability, and DHCP/IPAM
being present. Proxmox also ships new majors on its own schedule, and `docs/roadmap.md` had already
committed, in general terms, to "a compatibility validation task within one phase of each new PVE
release" — a promise this decision gives a concrete floor and mechanism.

## Decision

Target **PVE 8.2+ and 9.x** as the supported range today, with 10.x/11.x named as the forward
target for the v3.0 platform arc. Each new PVE major gets a validation pass within one phase of its
release. The mechanism behind that promise (T-2103, `docs/compatibility.md`) is a **mock-validated**
compatibility matrix: representative integration checks run against `internal/pvemock` through a
version-aware compat-server wrapper, on every commit, covering every version the policy names —
paired with an entirely separate, opt-in, **hardware-validated** channel
(`vnproxctl telemetry`/`verify`) that aggregates real results from clusters in the field.

## Consequences

**What this enables.** A stated, checkable compatibility promise instead of silent bit rot as PVE
moves forward — CI-speed regression coverage across every named version, with no cluster required
to run it on every commit.

**What this costs / forecloses.** The mock-validated matrix is explicitly **not** proof of real PVE
behavior — `docs/compatibility.md` says so in as many words: "nothing in this document should ever
be read as a claim that real Proxmox VE was involved" — so the compatibility promise this decision
makes is weaker than "we tested it" unless paired with genuine hardware validation, which this
project has narrow access to: one real two-node cluster (`vnprox-dev`), with SSH to only one of the
two nodes (`CLAUDE.md`). Every new PVE major is forced motion, not optional work a maintainer can
defer — "within one phase of its release" is a standing commitment that has to be re-earned every
time Proxmox ships, which is a real, recurring cost for a solo-maintained project
(`docs/adr/governance.md`). Committing to an 8.2+ floor also means carrying `ifupdown2`-era
assumptions indefinitely across the whole codebase — a future simplification that would only be
safe once the entire supported floor has moved past a given PVE behavior can't be taken until this
decision's floor moves. And the matrix models only *documented, checkable* divergences it has
actually found and verified against a running node — currently exactly one (SDN Fabrics, PVE
9.0+) — which means the matrix's coverage is only as good as what's been individually discovered
and confirmed, not a general guarantee that every difference between two PVE versions is caught.

## See also

- `docs/compatibility.md` (the mechanism, "Two matrices, not one").
- `docs/roadmap.md` (the original compatibility-policy commitment this decision fulfills).
- `internal/pvemock/compat_server.go`, `internal/pvemock/compat_versions.go`.
- `internal/flow/hostsample/ebpf.go` (an example of code written explicitly against this D9 range).
