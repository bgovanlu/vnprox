// SPDX-License-Identifier: Apache-2.0

// T-1404's guided dual-stack rollout wizard: adds an IPv6 subnet
// (addressing — RA/DHCPv6 itself is emitted by the zone's own dnsmasq/
// radvd once addressing exists on PVE SDN, docs/features/sdn.md §6's
// "IPv6 SLAAC management — display yes, config P1"; full RA/DHCPv6
// *parameter* control beyond addressing is out of scope here, same P1
// note) to an existing VLAN/VNet, built on T-603's blueprint.Instantiate
// pattern exactly like BlueprintsPage's own "pick -> fill params ->
// instantiate" flow — this wizard just fixes the blueprint and its target
// entity kind (sdn-subnet) instead of letting the operator choose either.
//
// Idempotent by construction: POST /blueprints/{id}/instantiate always
// re-derives its diff against current state (T-603's own contract —
// "entities that already match are skipped"), so running this wizard a
// second time against a VNet that already has the requested v6 subnet
// produces a changeset with zero ops, not a duplicate/conflicting one.
//
// T-3004: the wizard now reads GET /ipv6/segments and shows what IPv6 is
// actually live on the selected VNet before you add to it — a wizard that
// cannot read the state it edits is worse than no wizard.
//
// One subtlety worth keeping straight, because it inverts the obvious
// reading: **no observed segment is the normal precondition here, not an
// error.** GET /ipv6/segments carries one entry per interface where a
// router advertisement was actually observed, so a v4-only VNet — precisely
// what this wizard targets — has none. "No IPv6 observed" is therefore the
// starting state, and the VNet list deliberately comes from GET /sdn (the
// real VNet inventory) rather than from the segment list, which would
// otherwise offer only the VNets that need this wizard least.
import { useMemo, useState } from "react";
import type { Blueprint, BlueprintParamDef, BlueprintParamValue, Changeset, IPv6Segment } from "../api/types";
import { useBlueprintsQuery, useInstantiateBlueprintMutation, useSaveBlueprintMutation } from "../blueprints/queries";
import { ParamForm } from "../blueprints/ParamForm";
import { HelpAnchor } from "../help/HelpAnchor";
import { segmentsForVnet, useIPv6SegmentsQuery } from "./ipv6Queries";

/** Fixed, well-known id for this wizard's own backing blueprint — created
 * on first use (POST /blueprints upserts by id, docs/api.md's Blueprints
 * section: "id set to overwrite an existing saved one"), reused on every
 * subsequent run rather than each wizard invocation minting a new one. */
export const DUALSTACK_BLUEPRINT_ID = "vnprox-dualstack-ipv6-v1";

const DUALSTACK_PARAMS: BlueprintParamDef[] = [
  { name: "cidr", type: "cidr", label: "IPv6 subnet (CIDR)", description: "e.g. 2001:db8:20::/64", required: true },
  { name: "gateway", type: "ip", label: "Gateway", description: "Optional — the subnet's own IPv6 gateway address" },
  { name: "snat", type: "bool", label: "SNAT (egress to external)", default: true },
];

function dualStackBlueprint(): Blueprint {
  return {
    blueprintVersion: 1,
    id: DUALSTACK_BLUEPRINT_ID,
    name: "Dual-stack IPv6 rollout",
    description: "Adds an IPv6 subnet to an existing VLAN/VNet as one reviewable changeset (T-1404).",
    readOnly: false,
    nodeSelector: { mode: "all" },
    params: [{ name: "vnet", type: "string", required: true }, ...DUALSTACK_PARAMS],
    entities: [
      {
        kind: "sdn-subnet",
        idTemplate: "{{cidr}}",
        fields: { vnet: "{{vnet}}", cidr: "{{cidr}}", gateway: "{{gateway}}", snat: "{{snat}}" },
      },
    ],
  };
}

export interface DualStackVnetOption {
  id: string;
  alias?: string;
  zone: string;
}

export interface DualStackWizardProps {
  vnets: DualStackVnetOption[];
  /** Called once instantiation succeeds (whether or not it produced any
   * ops) — the parent decides what "open the changeset drawer" means. */
  onInstantiated?: (changeset: Changeset) => void;
}

type Phase = "form" | "submitting" | "done" | "error";

/** ensureBlueprint returns the existing dual-stack blueprint from
 * blueprintsData if already saved, else saves a fresh copy — never saves a
 * second time once one exists, so repeated wizard runs don't accumulate
 * duplicate saved blueprints. */
async function ensureBlueprint(
  existing: Blueprint | undefined,
  save: (bp: Blueprint) => Promise<Blueprint>,
): Promise<Blueprint> {
  if (existing) {
    return existing;
  }
  return save(dualStackBlueprint());
}

export function DualStackWizard({ vnets, onInstantiated }: DualStackWizardProps) {
  const [vnetId, setVnetId] = useState<string>(vnets[0]?.id ?? "");
  const [phase, setPhase] = useState<Phase>("form");
  const [lastChangeset, setLastChangeset] = useState<Changeset | undefined>(undefined);
  const [errorMessage, setErrorMessage] = useState<string | undefined>(undefined);

  const blueprintsQuery = useBlueprintsQuery();
  const segmentsQuery = useIPv6SegmentsQuery();
  const saveMutation = useSaveBlueprintMutation();
  const instantiateMutation = useInstantiateBlueprintMutation();

  const queriedBlueprint = useMemo(
    () => blueprintsQuery.data?.items.find((bp) => bp.id === DUALSTACK_BLUEPRINT_ID),
    [blueprintsQuery.data],
  );
  // ensuredBlueprint remembers this wizard instance's own successful
  // ensure-blueprint result across submits, independent of whether
  // useBlueprintsQuery's cached list has been refetched/invalidated yet
  // in between (POST /blueprints upserts by id — re-saving the identical
  // blueprint definition is itself idempotent, but there is no reason to
  // pay that round trip twice in the same wizard session).
  const [ensuredBlueprint, setEnsuredBlueprint] = useState<Blueprint | undefined>(undefined);

  async function handleSubmit(params: Record<string, BlueprintParamValue>): Promise<void> {
    if (!vnetId) {
      setErrorMessage("Pick a VLAN/VNet first.");
      setPhase("error");
      return;
    }
    setPhase("submitting");
    setErrorMessage(undefined);
    try {
      const bp = await ensureBlueprint(ensuredBlueprint ?? queriedBlueprint, (b) => saveMutation.mutateAsync(b));
      setEnsuredBlueprint(bp);
      const changeset = await instantiateMutation.mutateAsync({
        id: bp.id,
        req: { params: { ...params, vnet: vnetId }, title: `dual-stack IPv6: ${vnetId}` },
      });
      setLastChangeset(changeset);
      setPhase("done");
      onInstantiated?.(changeset);
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : "Could not instantiate the dual-stack rollout");
      setPhase("error");
    }
  }

  const selectedVnet = vnets.find((v) => v.id === vnetId);
  const observed = segmentsForVnet(segmentsQuery.data?.items ?? [], vnetId);

  return (
    <div className="flex flex-col gap-4" data-testid="dualstack-wizard">
      <div>
        <label htmlFor="dualstack-vnet" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
          VLAN / VNet
        </label>
        <p className="mt-0.5 flex items-center gap-1.5 text-xs text-fg-subtle">
          <span>Adds an IPv6 subnet to a VNet that has only IPv4 today.</span>
          <HelpAnchor topic="ipv6-dual-stack" />
        </p>
        <select
          id="dualstack-vnet"
          className="mt-1 w-full rounded-md border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
          value={vnetId}
          onChange={(e) => {
            setVnetId(e.target.value);
            setPhase("form");
          }}
        >
          {vnets.map((v) => (
            <option key={v.id} value={v.id}>
              {v.alias ? `${v.alias} (${v.id})` : v.id} — zone {v.zone}
            </option>
          ))}
        </select>
      </div>

      <ObservedIPv6
        vnetId={vnetId}
        segments={observed}
        loading={segmentsQuery.isLoading}
        failed={segmentsQuery.error !== null}
        partial={segmentsQuery.data?.partial === true}
        failedNodes={segmentsQuery.data?.failedNodes ?? []}
      />

      <ParamForm
        blueprintId={DUALSTACK_BLUEPRINT_ID}
        params={DUALSTACK_PARAMS}
        nodesValue=""
        onNodesChange={() => undefined}
        onValidSubmit={(params) => {
          void handleSubmit(params);
        }}
        submitLabel="Roll out IPv6"
        submitting={phase === "submitting"}
      />

      {phase === "done" && lastChangeset ? (
        <p role="status" data-testid="dualstack-result">
          {lastChangeset.ops.length === 0
            ? `${selectedVnet?.id ?? vnetId} is already up to date — no changes needed.`
            : `Draft changeset ${lastChangeset.id} created with ${String(lastChangeset.ops.length)} op(s) for ${selectedVnet?.id ?? vnetId}.`}
        </p>
      ) : null}

      {phase === "error" && errorMessage ? (
        <p role="alert" data-testid="dualstack-error">
          {errorMessage}
        </p>
      ) : null}
    </div>
  );
}

/** What IPv6 is already live on the VNet about to be edited, read from
 * GET /ipv6/segments.
 *
 * Three distinct states, kept distinct on purpose:
 *
 *   - the read failed, or the cluster fan-out was partial — say so, and do
 *     not claim there is no IPv6. A failed read is not an observation.
 *   - the read succeeded and observed nothing — the ordinary v4-only
 *     starting point this wizard exists for.
 *   - the read observed router advertisements — show them, so an operator
 *     adding a second prefix is doing it knowingly.
 */
function ObservedIPv6({
  vnetId,
  segments,
  loading,
  failed,
  partial,
  failedNodes,
}: {
  vnetId: string;
  segments: IPv6Segment[];
  loading: boolean;
  failed: boolean;
  partial: boolean;
  failedNodes: string[];
}) {
  if (!vnetId) {
    return null;
  }
  if (loading) {
    return <p className="text-xs text-fg-muted">Checking what IPv6 is live on {vnetId}…</p>;
  }
  if (failed) {
    return (
      <p role="status" data-testid="dualstack-observed" className="text-xs text-amber-700 dark:text-amber-400">
        Could not read the current IPv6 state of {vnetId}. That is a failed read, not an absence of IPv6 — check what is
        already configured before rolling out.
      </p>
    );
  }

  return (
    <div data-testid="dualstack-observed" className="rounded-md border border-border p-2 text-xs">
      <h4 className="font-medium text-fg-body">IPv6 observed on {vnetId} today</h4>
      {segments.length === 0 ? (
        <p className="mt-1 text-fg-subtle">
          No router advertisement observed on this VNet — the ordinary starting point for a dual-stack rollout.
        </p>
      ) : (
        <ul className="mt-1 flex flex-col gap-0.5">
          {segments.map((s) => (
            <li key={`${s.node}-${s.iface}`} className="text-fg-muted">
              <span className="font-mono">
                {s.node}/{s.iface}
              </span>
              {": "}
              {s.raPresent ? "RA present" : "no RA"}
              {s.prefixes && s.prefixes.length > 0 ? ` · ${s.prefixes.join(", ")}` : ""}
              {s.dhcpv6ServerPresent ? " · DHCPv6 server" : ""}
              {s.managedFlag ? " · managed flag" : ""}
            </li>
          ))}
        </ul>
      )}
      {partial && (
        <p className="mt-1 text-amber-700 dark:text-amber-400">
          Partial read: {failedNodes.length > 0 ? failedNodes.join(", ") : "one or more nodes"} did not answer, so this
          list may be incomplete.
        </p>
      )}
    </div>
  );
}
