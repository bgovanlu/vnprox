# T-701 root-cause analysis — "SDN creation fails because there is no gateway"

Triage date: 2026-07-12 (pre-implementation investigation for the T-701 card in
`planning/tasks/phase-7.md`).

**Summary: "no gateway" is not caught by vnprox anywhere today. Against pvemock nothing fails
(which is why the gap shipped); against real PVE the failure is a mid-apply PVE API rejection for
the SNAT case, and silently-broken traffic post-apply for the EVPN/routed case.** There is also a
second, unrelated bug whose error text literally says "no gateway" and matches the user's wording
— both are documented below.

## 1. The wizards treat the gateway as optional free text, with no default and no coupling to SNAT

- `web/src/sdn/wizards/SimpleZoneWizard.tsx:119` — the subnet step is `isValid: true`
  unconditionally; lines 126–138 render a free-text Gateway field (empty default) and a SNAT
  checkbox that can be enabled with no gateway. The same copy-pasted block exists in all five
  wizards (`VlanZoneWizard.tsx:168`, `QinqZoneWizard.tsx:159`, `VxlanZoneWizard.tsx:201`,
  `EvpnZoneWizard.tsx:183`).
- `web/src/sdn/wizards/strings.ts:29` — the help text says the gateway is "usually the first
  address in the range", but nothing computes or pre-fills it.
- `web/src/sdn/wizards/wizardOps.ts:35-46,67` — the drafted op carries
  `gateway: p.subnetGateway ?? ""` verbatim; empty stays empty.

## 2. No vnprox validator requires a gateway or cross-checks it

- `internal/change/validate_schema.go:92-97` — `validIP("") == true`, so the only gateway check
  (line 315, format-only via `schemaIP`) passes an empty gateway. There is **no**
  gateway-in-CIDR check, **no** SNAT-requires-gateway check.
- `internal/change/validate_sdn.go:51-61` — the SDN validator class covers only bridge existence
  and tag uniqueness.
- `internal/change/validate_advisory.go:101-109` — subnet advisories cover only DHCP-range
  overlap. Result: the draft validates clean; **it is not a vnprox validation finding today**.

## 3. pvemock is more permissive than real PVE, hiding the gap from CI

- `internal/pvemock/sdn.go:360-385` — `handleSDNSubnetCreate` accepts any subnet: no
  SNAT-requires-gateway rule, no gateway-inside-CIDR rule, and it does not auto-register the
  gateway IPAM record real PVE creates (fixtures hand-model those records instead, e.g.
  `testdata/clusters/evpn-lab.yaml:266`, `three-node-vlan.yaml:311`).

## 4. Where the failure actually lands, per case

- The wire type omits the empty gateway (`internal/pve/types.go:215`, `gateway,omitempty`;
  passthrough at `cmd/vnproxd/changeagent.go:304-310`), so real PVE sees a gatewayless subnet.
- **SNAT + no gateway:** real PVE's `pve-network` SubnetPlugin rejects SNAT without a gateway
  (and a gateway outside the CIDR) at subnet create/update — i.e. a **PVE API rejection at the
  `StepSDNStage` step mid-apply** (`internal/change/apply_exec.go:108-121`), failing the
  changeset and triggering same-request SDN rollback. Exact rejection point/message per PVE
  version **needs hardware validation** (no live cluster in this environment).
- **EVPN subnet, no gateway, no SNAT:** PVE accepts the config; the anycast gateway is simply
  never realized, so routed/exit-node traffic is **silently broken post-apply** — no finding, no
  error.
- **Simple zone, no gateway, no SNAT:** legitimately optional (an isolated network); the wizard
  copy already frames this correctly.
- **VLAN/QinQ/VXLAN:** gateway is external-router-provided; optional in PVE, only meaningful for
  the DHCP router option and IPAM.

## 5. Secondary root cause — the error that literally says "no gateway"

`internal/api/changesets.go:199-202` swallows gateway-resolution failure
(`gw, _ = gateways.GatewayFor(r.Context())`). If the session's PVE client is unavailable
(expired/unrenewable ticket), the apply proceeds anyway and dies mid-apply at
`internal/change/apply_exec.go:110-112` with `"no PVE gateway available for sdn stage op (no
user session)"` — a failed changeset whose user-visible explanation is "there is no gateway".
Highly plausible as the exact user report; the fix is a fail-fast pre-check, included in the
card (`pve_session_required`).

## Contract changes required

- **docs/api.md:** additive only — new validation finding codes on the generic finding shape
  (`{severity, code, message, ref?, fix?}`, api.md line 84); one new stable apply-rejection
  error code (`pve_session_required`) on `POST /changesets/{id}/apply` documented retroactively
  per docs/development.md definition-of-done #4. No shape changes.
- **docs/data-model.md:** none — `SdnSubnet` already carries `gateway`/`snat` (§2 line 57); a
  semantics note on zone-type gateway requirements is additive.

## Needs hardware validation

1. Exact real-PVE (8.2/9.x) behavior for SNAT-without-gateway and gateway-outside-CIDR:
   rejected at subnet POST/PUT (expected) vs. at `PUT /cluster/sdn` apply, and the exact error
   strings.
2. Whether PVE registers the gateway's IPAM record at subnet create or at SDN apply (pvemock
   fidelity work depends on this).
3. EVPN anycast-gateway realization when the gateway is absent: does zone status report an
   error, or is traffic just dark?
4. Whether simple-zone SNAT additionally depends on `net.ipv4.ip_forward` being set by PVE.
