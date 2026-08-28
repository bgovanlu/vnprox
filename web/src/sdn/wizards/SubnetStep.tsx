// SPDX-License-Identifier: Apache-2.0

// Shared "Addresses" step for all five zone wizards (T-701): extracts the
// copy-pasted subnet block every wizard used to carry its own copy of
// (SimpleZoneWizard.tsx/VlanZoneWizard.tsx/QinqZoneWizard.tsx/
// VxlanZoneWizard.tsx/EvpnZoneWizard.tsx, before this task) into one place,
// with three fixes the T-701 root-cause analysis flagged:
//
//  1. The gateway is pre-filled to the CIDR's first usable address as the
//     user types the CIDR, instead of staying empty free text — reusing
//     web/src/ipam/nextFree.ts's firstUsableIPv4 for a brand-new CIDR, or
//     (T-405's shared-component contract) the live allocation grid's own
//     nextFreeAddress when the typed CIDR overlaps a subnet vnprox already
//     has IPAM data for, so the suggestion never contradicts what the grid
//     shows and never suggests an address that's already taken.
//  2. "No gateway" is an explicit, named choice ("keep this network
//     isolated") instead of a silently-empty field the user might have
//     just not gotten to yet.
//  3. SNAT is disabled (with a plain-English reason) until a gateway is
//     set, and gets zone-type-specific copy explaining what the gateway
//     actually means for this zone type (docs/features/sdn.md §2).
import { useEffect, useRef } from "react";
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { firstUsableIPv4 } from "../../ipam/nextFree";
import { useIpamAllocationsQuery } from "../../ipam/queries";
import { wizardStrings } from "./strings";
import { cidrError, gatewayError } from "./validation";

const S = wizardStrings;

export type SubnetZoneType = "simple" | "vlan" | "qinq" | "vxlan" | "evpn";

export interface SubnetStepValue {
  cidr: string;
  gateway: string;
  /** True once the user has explicitly chosen "keep isolated" for the
   * current cidr — distinct from an empty gateway the user just hasn't
   * gotten to yet, so a re-render (or the live-allocation-grid effect
   * below) never silently re-fills a deliberately-cleared gateway. */
  isolated: boolean;
  snat: boolean;
}

// The zero value belongs next to its type; react-refresh's
// only-export-components rule flags any non-component export from a
// component file (same trade-off as Toast.tsx's useToast).
// eslint-disable-next-line react-refresh/only-export-components
export const emptySubnetStepValue: SubnetStepValue = { cidr: "", gateway: "", isolated: false, snat: false };

export interface SubnetStepProps {
  zoneType: SubnetZoneType;
  value: SubnetStepValue;
  onChange: (next: SubnetStepValue) => void;
  /** EVPN only: the wizard's own live exit-node selection, cross-checked
   * against SNAT here (docs/features/sdn.md §2: "SNAT additionally
   * requires at least one exit node, cross-checked against the wizard's
   * own exitNodes selection live"). Undefined for every other zone type. */
  evpnExitNodeCount?: number;
}

/** SubnetStep is deliberately controlled with no internal state of its own
 * (besides the read-only IPAM query below) — every wizard already has its
 * own subnetCidr/subnetGateway/snat state; this component just renders and
 * edits it through `value`/`onChange`, the same pattern every other shared
 * wizard piece (NodeCheckboxList, WizardPreviewPane) already follows. */
export function SubnetStep({ zoneType, value, onChange, evpnExitNodeCount }: SubnetStepProps) {
  const { cidr, gateway, isolated, snat } = value;

  // T-405's shared allocation query (NextFreePicker's own data source): when
  // the typed CIDR overlaps a subnet vnprox already has IPAM data for, its
  // lowest free address (the first collapsed free range's start, skipping
  // every known allocation) is the right suggestion instead of the naive
  // "network + 1" guess.
  const { data: allocations } = useIpamAllocationsQuery(cidr || undefined);
  const gridSuggestion = allocations?.freeRanges[0]?.start;

  // lastAutoFillRef tracks the last value this component itself wrote into
  // `gateway` (from either source below) — an update only overwrites the
  // field when the current value still matches what *this component* last
  // set, so the user's own edit always wins, matching NextFreePicker's own
  // "suggest, never force" convention.
  const lastAutoFillRef = useRef<string | undefined>(undefined);

  function setCidr(nextCidr: string): void {
    const next: SubnetStepValue = { ...value, cidr: nextCidr };
    if (!isolated) {
      const guess = nextCidr ? firstUsableIPv4(nextCidr) : undefined;
      next.gateway = guess ?? "";
      lastAutoFillRef.current = guess;
    }
    onChange(next);
  }

  // Once the live allocation grid resolves for the current CIDR, refine an
  // auto-filled gateway to the grid's own "skip known allocations"
  // suggestion — never touching a gateway the user typed themselves.
  useEffect(() => {
    if (isolated || !cidr || !gridSuggestion) return;
    if (gridSuggestion === gateway) return;
    if (gateway !== "" && gateway !== lastAutoFillRef.current) return;
    lastAutoFillRef.current = gridSuggestion;
    onChange({ ...value, gateway: gridSuggestion });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- onChange/value intentionally excluded: this effect only reacts to the grid resolving or the cidr/isolated flag changing, not to every parent re-render.
  }, [gridSuggestion, cidr, isolated]);

  function setGateway(nextGateway: string): void {
    lastAutoFillRef.current = undefined; // the user is now editing directly
    onChange({ ...value, gateway: nextGateway });
  }

  function setIsolated(nextIsolated: boolean): void {
    if (nextIsolated) {
      lastAutoFillRef.current = undefined;
      onChange({ ...value, isolated: true, gateway: "", snat: false });
      return;
    }
    const guess = firstUsableIPv4(cidr) ?? "";
    lastAutoFillRef.current = guess || undefined;
    onChange({ ...value, isolated: false, gateway: guess });
  }

  function setSnat(nextSnat: boolean): void {
    onChange({ ...value, snat: nextSnat });
  }

  const snatDisabled = gateway === "";
  const evpnSnatMissingExitNode = zoneType === "evpn" && snat && (evpnExitNodeCount ?? 0) === 0;
  const cidrErr = cidrError(cidr);
  const gatewayErr = isolated ? undefined : gatewayError(gateway, cidr);

  return (
    <div className="space-y-3">
      <p className="text-slate-600 dark:text-slate-300">{S.common.subnetSkipHelp}</p>
      <Field label="Address range (CIDR)" help={S.common.cidrHelp} errors={cidrErr ? [cidrErr] : undefined}>
        <input
          className={inputClass}
          value={cidr}
          onChange={(e) => {
            setCidr(e.target.value);
          }}
          placeholder="10.50.0.0/24"
        />
      </Field>
      {cidr && (
        <>
          <div className="space-y-1.5" role="radiogroup" aria-label="Gateway">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name={`subnet-gateway-mode-${zoneType}`}
                checked={!isolated}
                onChange={() => {
                  setIsolated(false);
                }}
              />
              {S.common.gatewayModeHasGateway}
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name={`subnet-gateway-mode-${zoneType}`}
                checked={isolated}
                onChange={() => {
                  setIsolated(true);
                }}
              />
              {S.common.gatewayModeIsolated}
            </label>
          </div>

          {isolated ? (
            <p className="text-xs text-fg-subtle">{S.common.gatewayModeIsolatedHelp}</p>
          ) : (
            <>
              <Field label="Gateway" help={S.common.gatewayZoneCopy[zoneType]} errors={gatewayErr ? [gatewayErr] : undefined}>
                <input
                  className={inputClass}
                  value={gateway}
                  onChange={(e) => {
                    setGateway(e.target.value);
                  }}
                  placeholder="10.50.0.1"
                />
              </Field>
              <Field label="Internet access (SNAT)" help={snatDisabled ? S.common.snatDisabledNoGateway : S.common.snatHelp}>
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={snat}
                    disabled={snatDisabled}
                    onChange={(e) => {
                      setSnat(e.target.checked);
                    }}
                  />
                  Enable SNAT
                </label>
              </Field>
              {evpnSnatMissingExitNode && <p className="text-xs text-amber-600 dark:text-amber-400">{S.common.evpnSnatNeedsExitNode}</p>}
            </>
          )}
        </>
      )}
    </div>
  );
}
