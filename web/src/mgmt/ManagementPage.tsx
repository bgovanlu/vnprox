// The dedicated "Management" surface (issue #1: "I don't see the management
// interfaces anywhere. app needs to be able to configure all aspects of
// management interfaces"). T-702 made a node's management path *visible* in
// the topology (badges + inspector section) but there was no one place to
// find and configure every node's management interface. This page lists,
// per node, each carrier of a management IP or corosync link — its resolved
// aspects (addresses, gateway, VLAN, MTU, comments, physical path,
// redundancy) — and offers the two write paths that already exist: the full
// entity editor (which edits addresses/gateway/VLAN/bond/comments/MTU) and
// the guided redundancy wizard. Re-addressing the management IP itself stays
// out of scope by construction (guarded by the T-203 net-effect interlock);
// everything else about a management interface is reachable from here.
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Tooltip } from "../components/Tooltip";
import { useSession } from "../api/useSession";
import type { EntityDetail, ManagementPathRef, MgmtRole } from "../api/types";
import { capsForNode, missingCapTooltip } from "../changesets/capabilities";
import { editorKindForInventoryKind, useEditorLauncherStore } from "../changesets/editorLauncherStore";
import { resumeOnboarding } from "../onboarding/onboardingMachine";
import { useOnboardingProgressQuery, useSaveOnboardingProgressMutation } from "../onboarding/queries";
import { useInventoryDetailQuery, useMgmtStatusQuery } from "../topology/queries";
import { describeMgmtPathRedundancy } from "./mgmtPath";
import { useMgmtWizardStore } from "./mgmtWizardStore";
import { mgmtStrings } from "./strings";

const ROLE_LABEL: Record<MgmtRole, string> = {
  mgmt: "Management IP",
  corosync: "Corosync link",
};

/** Reads a string field from an EntityDetail.fields map (all values are
 * `unknown`; the backend stores these as strings — see internal/inventory's
 * fieldMap). Empty/absent → "". */
function fieldStr(detail: EntityDetail | undefined, key: string): string {
  const v = detail?.fields[key];
  return typeof v === "string" ? v : "";
}

function AspectRow({ label, value }: { label: string; value: string }) {
  if (!value) return null;
  return (
    <div className="flex gap-2">
      <span className="w-24 shrink-0 text-slate-600 dark:text-slate-400">{label}</span>
      <span className="min-w-0 break-words font-medium text-slate-700 dark:text-slate-200">{value}</span>
    </div>
  );
}

/** One management carrier for a node: its resolved aspects plus the two edit
 * affordances (full editor + redundancy wizard). */
function MgmtCarrierCard({ node, pathRef }: { node: string; pathRef: ManagementPathRef }) {
  const { data: detail } = useInventoryDetailQuery(pathRef.ref);
  const { data: session } = useSession();
  const openEditor = useEditorLauncherStore((s) => s.open);
  const openMgmtWizard = useMgmtWizardStore((s) => s.open);

  const editorKind = editorKindForInventoryKind(detail?.kind);
  const canWrite = capsForNode(session, node).netWrite;
  const editDisabledReason = missingCapTooltip(session, node, "netWrite");

  const vid = fieldStr(detail, "vid");
  const parentName = fieldStr(detail, "parentName");

  const editButton = (
    <Button
      size="sm"
      variant="secondary"
      disabled={!editorKind || !canWrite}
      onClick={() => {
        if (editorKind) openEditor({ kind: editorKind, node, target: pathRef.ref });
      }}
    >
      Edit interface
    </Button>
  );

  return (
    <div className="rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <div className="mb-2 flex items-center gap-2">
        <span className="font-medium text-slate-800 dark:text-slate-100">{detail?.label ?? pathRef.ref}</span>
        {pathRef.roles.map((role) => (
          <span
            key={role}
            className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold text-amber-800 dark:bg-amber-900/50 dark:text-amber-200"
          >
            {ROLE_LABEL[role]}
          </span>
        ))}
      </div>

      <div className="space-y-1 text-xs">
        <AspectRow label="Node" value={node} />
        <AspectRow label="Interface" value={detail?.kind ?? ""} />
        <AspectRow label="Addresses" value={fieldStr(detail, "addresses")} />
        <AspectRow label="Gateway" value={fieldStr(detail, "gateway")} />
        {vid && vid !== "0" && <AspectRow label="VLAN" value={parentName ? `${vid} on ${parentName}` : vid} />}
        <AspectRow label="MTU" value={fieldStr(detail, "mtu")} />
        <AspectRow label="Comments" value={fieldStr(detail, "comments")} />
        <AspectRow
          label="Physical path"
          value={pathRef.path.length > 0 ? pathRef.path.join(" → ") : "(no physical interface resolved)"}
        />
      </div>

      <p className="mt-2 text-xs text-slate-600 dark:text-slate-300">
        {describeMgmtPathRedundancy(pathRef.path, pathRef.redundant)}
      </p>

      <div className="mt-3 flex flex-wrap gap-2">
        {editDisabledReason ? <Tooltip content={editDisabledReason}>{editButton}</Tooltip> : editButton}
        {canWrite && (
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              openMgmtWizard({ node });
            }}
          >
            {mgmtStrings.launch.button}
          </Button>
        )}
      </div>
    </div>
  );
}

export function ManagementPage() {
  const { data: mgmtStatus, isLoading, isError } = useMgmtStatusQuery();
  const { data: onboardingProgress } = useOnboardingProgressQuery();
  const saveOnboarding = useSaveOnboardingProgressMutation();

  const nodes = mgmtStatus ? Object.keys(mgmtStatus.nodes).sort() : [];
  const hasAny = nodes.some((n) => (mgmtStatus?.nodes[n]?.length ?? 0) > 0);

  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <PageHeader
        title="Management interfaces"
        description="The interface on each node that carries its management IP (and any corosync links), plus everything you can
          change about it. Editing an interface here stages a normal changeset — nothing applies until you review and
          confirm it, exactly like every other change in vnprox."
      />

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading management interfaces…</p>}
      {isError && <p className="text-sm text-red-600 dark:text-red-400">Could not load the management-interface status.</p>}

      {mgmtStatus?.staleProtected && (
        <div className="rounded border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          The protected-interface list looks out of date — a management interface moved since it was last confirmed.
          Re-confirm it in the onboarding &quot;protected interfaces&quot; step.
        </div>
      )}

      {mgmtStatus?.source === "detected" && (
        <div className="rounded border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          <p>
            This is vnprox&apos;s best-effort detection (from each node&apos;s IP and corosync.conf) — no one has
            confirmed it during onboarding yet, so double-check it before relying on it.
          </p>
          {onboardingProgress && (
            <button
              type="button"
              className="mt-1 font-medium underline hover:no-underline"
              onClick={() => {
                saveOnboarding.mutate({ ...resumeOnboarding(onboardingProgress), currentStep: "protected" });
              }}
            >
              Review protected interfaces
            </button>
          )}
        </div>
      )}

      {mgmtStatus && !hasAny && (
        <EmptyState
          title="No management interfaces resolved"
          description="vnprox hasn't resolved a management or corosync carrier on any node yet. Confirm your protected interfaces in onboarding, or check that the cluster inventory has finished loading."
        />
      )}

      {nodes.map((node) => {
        const paths = mgmtStatus?.nodes[node] ?? [];
        if (paths.length === 0) return null;
        return (
          <section key={node} className="space-y-2">
            <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">{node}</h2>
            <div className="space-y-2">
              {paths.map((p) => (
                <MgmtCarrierCard key={p.ref} node={node} pathRef={p} />
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}
