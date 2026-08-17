# Bug: `internal/sim` cannot resolve a guest NIC attached to a real SDN vnet

**Found:** 2026-08-17, incidentally, by the agent implementing T-3103 (VNet-scope
firewall). Not part of that card's scope — filed separately so it doesn't
evaporate as chat output. Not yet triaged into a task card.

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

## Scope estimate (unverified — from the discovering agent's report, not independently confirmed)

Fixing `endpoint.go:161` to key by the bare name (matching `link.go`'s and
`sim/engine.go`'s own convention) is likely the correct one-line fix, but the
agent flagged 16+ existing tests in `internal/sim` as depending on the
bug-compatible fixture shape — those fixtures would need to switch to
minting Refs the same composite way `ingest.go` does, or the fix needs a
compatibility shim, before this is safe to land. Whoever picks this up
should scope it properly rather than trust this estimate.

## Recommendation

Needs its own task card. Priority judgment call for the person triaging it:
this is a simulator correctness bug in the product's core "does the honesty
contract hold" promise (`docs/features/firewall.md` §5's "No silent
approximations" bar) — arguably P0/P1 rather than routine backlog, but that
call belongs to whoever owns the roadmap, not to the agent that found it in
passing.
