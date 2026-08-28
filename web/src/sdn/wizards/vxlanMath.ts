// SPDX-License-Identifier: Apache-2.0

// The VXLAN wizard's "MTU math shown explicitly" requirement
// (docs/features/sdn.md §2), reusing T-402's own derivation rather than
// reimplementing it: internal/change/validate_sdn.go's underlayMTU (1500)
// and vxlanOverhead (50) constants, and validate_advisory.go's
// checkVxlanMTU warning condition (`mtu > underlayMTU - vxlanOverhead`).
// These are plain numeric literals here (TypeScript can't import Go
// constants) — kept in exact lockstep with those two Go files by doc
// comment cross-reference; if either constant ever changes there, this
// file needs the matching edit (T-402's own report flags underlayMTU as
// "the number real PVE's own VXLAN zone default (1450) assumes").

/** Assumed default underlay path MTU (standard Ethernet) — mirrors
 * internal/change/validate_sdn.go's `underlayMTU`. */
export const UNDERLAY_MTU = 1500;

/** VXLAN's encapsulation header size — mirrors validate_sdn.go's
 * `vxlanOverhead`. */
export const VXLAN_OVERHEAD = 50;

/** The PVE-recommended safe VXLAN zone MTU — mirrors
 * validate_advisory.go's `checkVxlanMTU`'s `safe := underlayMTU -
 * vxlanOverhead` (1500 - 50 = 1450), the exact figure T-402's own
 * acceptance criterion 3 names ("1500 underlay + vnet MTU 1500 → warning
 * with fix patch (set 1450)"). */
export const VXLAN_SAFE_MTU = UNDERLAY_MTU - VXLAN_OVERHEAD;

export interface VxlanMtuDerivation {
  underlayMtu: number;
  overhead: number;
  safeMtu: number;
  /** The MTU as entered in the wizard; 0 means "left blank" (PVE applies
   * its own default), mirroring checkVxlanMTU's own `mtu == 0` early-out. */
  requestedMtu: number;
  /** True iff requestedMtu leaves no headroom for VXLAN's overhead —
   * exactly checkVxlanMTU's `mtu > safe` condition. */
  warn: boolean;
}

/** Computes the VXLAN MTU derivation for the wizard's live "why this
 * number" display (docs/features/sdn.md §2: "MTU math shown explicitly
 * (underlay MTU − 50)"). `underlayMtu` defaults to UNDERLAY_MTU — the same
 * "no better signal exists yet" simplification validate_sdn.go's own doc
 * comment documents (no per-peer-route MTU discovery in this codebase). */
export function computeVxlanMtuDerivation(requestedMtu: number, underlayMtu: number = UNDERLAY_MTU): VxlanMtuDerivation {
  const safeMtu = underlayMtu - VXLAN_OVERHEAD;
  return {
    underlayMtu,
    overhead: VXLAN_OVERHEAD,
    safeMtu,
    requestedMtu,
    warn: requestedMtu !== 0 && requestedMtu > safeMtu,
  };
}
