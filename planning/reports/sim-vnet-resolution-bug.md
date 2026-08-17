# Bug: `internal/sim` cannot resolve a guest NIC attached to a real SDN vnet — FIXED 2026-08-17

**Found:** 2026-08-17, incidentally, by the agent implementing T-3103 (VNet-scope
firewall). Not part of that card's scope — filed separately so it doesn't
evaporate as chat output. **Fixed the same day**, on triage, once the actual
scope turned out to be a contained, one-map fix rather than the 16-test
migration the discovering agent's report estimated (see "What actually
shipped" at the bottom) — no separate task card was needed.

## The defect

`internal/inventory/ingest.go`'s vnet ingestion deliberately splits a VNet's
identity into two different strings, on purpose, with the split itself
documented:

```go
// Ref.ID is the documented "zone/vnet" composite (see docs/api.md's
// sdn-vnet::zone1/vnet1 example); the bare VNet name is kept in the
// ID field for guest-attachment lookups.
vnet := &SdnVnet{
    Ref: Ref{Kind: KindSDNVnet, ID: n.Zone + "/" + n.ID},
    ID:  n.ID,
    ...
```

- `SdnVnet.Ref.ID` — the composite `"<zone>/<vnet>"` form (T-3101's
  established convention for every consumer that displays or targets a vnet).
- `SdnVnet.ID` (the struct field, distinct from `.Ref.ID`) — the bare vnet
  name, exactly so `internal/inventory/link.go`'s guest-attachment resolution
  can match it against a guest's raw `bridge=<name>` config value, which PVE
  never qualifies with a zone.

Two `vnetByID` maps downstream key off that bare-name field, correctly:

- `internal/inventory/link.go:82`: `vnetByID[v.ID] = ref` (bare name -> Ref).
  `resolveGuestNic` (line 274) looks this up by `nic.TargetName`, itself a
  bare name from the guest's config — consistent, correct.
- `internal/sim/engine.go:74`: `e.vnetByID[v.ID] = v` (bare name ->
  `*inventory.SdnVnet`) — same convention, built independently in `internal/sim`.

But `internal/sim/endpoint.go:161` looks the map up the other way:

```go
rep.vnet = e.vnetByID[nic.BridgeOrVnet.ID]
```

`nic.BridgeOrVnet` is the *Ref* `link.go`'s `resolveGuestNic` already
resolved for this NIC — so `.ID` here is the **composite** `"<zone>/<vnet>"`
form, not the bare name `e.vnetByID` is keyed by. The lookup can never hit
for a guest genuinely attached to a real SDN vnet: `e.vnetByID["zone1/vnet1"]`
against a map whose only keys are `"vnet1"`.

## Why it's real and not just a naming quirk

`rep.vnet` is how `internal/sim` learns a guest endpoint's zone/subnet
membership for L3 evaluation. If it silently resolves to `nil` for every
real vnet attachment, the simulator falls back to whatever `rep.vnet == nil`
means downstream — which, per `docs/features/firewall.md` §5's honesty
contract ("No silent approximations... explicitly reported as 'not
evaluated'"), it may not even be doing; this needs to be checked, not
assumed, before calling the blast radius "just wrong subnet info."

## Why it's hidden today

`internal/sim/unit_test.go` and `internal/sim/world_test.go` build their test
fixtures' vnet `Ref`s directly, by hand, using the *bare* name as `Ref.ID` —
never going through `internal/inventory/ingest.go`'s real composite-ID
minting. Every existing sim test is therefore bug-compatible with the broken
lookup, which is why `go test ./internal/sim/...` is green despite the bug.
The T-3103 agent confirmed this and worked around it for its own new test
(`noteVNetFirewall`) with a direct unit test rather than a full fixture-based
case, specifically to avoid exercising this path.

## Scope estimate that turned out to be too pessimistic

The discovering agent's report guessed the fix needed either re-keying
`endpoint.go:161` to the bare name (which would have required threading a
zone lookup through a call site that doesn't have one) or migrating 16+
existing `internal/sim` tests off their bug-compatible bare-ID fixtures. On
triage, the actual fix went the other direction and was smaller: add a
second index, `Engine.vnetByRef map[inventory.Ref]*inventory.SdnVnet`
(keyed by the vnet's full `Ref`, exactly the pattern `bridgesByRef` already
used three lines above the bug in the same function), and look a NIC's
vnet attachment up by `nic.BridgeOrVnet` — the full Ref, composite ID and
all — instead of re-deriving a bare name from it. No zone lookup, no
compatibility shim.

That fix also made it *safe* to correct `world_test.go`'s `vnet()` helper
to mint the real `"<zone>/<vnet>"` composite `Ref.ID` (previously bare,
matching the bug) instead of leaving it bug-compatible — done in the same
change, and every existing vnet-attached test in the suite (the
`TestVerdictMatrix` evpn/vxlan-zone cases included) now exercises the real
production lookup path and still passes, which is a stronger proof the fix
is correct than an isolated regression test would have been.

## What actually shipped

- `internal/sim/engine.go`: new `vnetByRef` index, populated alongside the
  existing bare-name `vnetByID` (which stays — `resolveIP`'s
  `sub.Vnet`-keyed lookup at `endpoint.go:86` still legitimately needs bare
  names, and `link.go`'s own same-named map still needs them for matching a
  guest's raw `bridge=` config value).
- `internal/sim/endpoint.go:161`: `rep.vnet = e.vnetByRef[nic.BridgeOrVnet]`,
  replacing the broken `e.vnetByID[nic.BridgeOrVnet.ID]`.
- `internal/sim/world_test.go`: `vnet()` helper now mints the real composite
  `Ref.ID`, so the whole existing suite validates the fix rather than merely
  failing to contradict it.
- `internal/sim/unit_test.go`: the stale comment on
  `TestNoteVNetFirewall_DisclosesEnabledForwardChain` (written when the bug
  was still open) updated to stop describing it as unresolved.
- Verified: `go build`, `go vet`, full `go test ./...`, frontend `tsc`
  + `vitest` (2060 tests), and `make check` (lint/govulncheck/npm audit) all
  clean.
