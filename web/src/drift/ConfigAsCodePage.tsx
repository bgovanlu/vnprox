// T-3001: the config-as-code cockpit.
//
// Four routes' worth of machinery — the git sync, the spec document, the
// import plan, and the drift reconciliation actions — were reachable only with
// `curl` before this screen. They are one workflow, so they are one screen,
// and it speaks the product's own vocabulary rather than the API's:
//
//   spec    the declarative document — what this cluster is supposed to be
//   config  /etc/network/interfaces as PVE reports it
//   live    the running kernel
//
// What this screen deliberately does NOT have:
//
//   * an apply path. Every action here either stages a draft changeset the
//     operator takes through the ordinary review screen, or moves the
//     document. Nothing on this page applies, confirms or rolls back.
//   * a "reconcile" button. Restoring intent and adopting reality have
//     opposite blast radii; they are separate controls with separate
//     confirmations, and the daemon audits them separately.
//   * a control that turns the git sync on. That is `vnprox.toml`, and no
//     route exists for it.
import { useState } from "react";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { HelpAnchor } from "../help/HelpAnchor";
import { GitSyncPanel } from "./GitSyncPanel";
import { SpecDocumentPanel } from "./SpecDocumentPanel";
import { SpecPlanPanel } from "./SpecPlanPanel";
import { ThreeWayPanel } from "./ThreeWayPanel";
import { gitSyncState, specPresence } from "./gitsyncState";
import { POSITIONS, POSITION_LABEL, POSITION_MEANING } from "./positions";
import { useGitSyncStatusQuery, useSpecPinQuery } from "./queries";

export function ConfigAsCodePage() {
  const gitSyncQuery = useGitSyncStatusQuery();
  const pinQuery = useSpecPinQuery();
  const { data: session } = useSession();
  const [specDocument, setSpecDocument] = useState("");

  const gitSync = gitSyncState(gitSyncQuery.data, gitSyncQuery.isLoading, gitSyncQuery.error);
  const presence = specPresence(gitSync, pinQuery.data, pinQuery.error);

  // Every write on this screen is cluster-wide (a pin, a spec import, a
  // reconciliation on an entity that may live on any node), so it is gated on
  // hasAnyCap rather than one node's capsForNode — the same reasoning the
  // drift-fix button and the onboarding walkthrough already use.
  const canWrite = hasAnyCap(session, "netWrite");
  const writeDisabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  return (
    <div className="flex h-full flex-col gap-6 overflow-y-auto">
      <div>
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          Config as code
          <HelpAnchor topic="config-as-code-page" />
        </h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Where the declared intent, the on-disk configuration and the running kernel are compared — and the two
          ways of resolving a disagreement between them. Everything here stages; nothing here applies.
        </p>
        <dl className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-3">
          {POSITIONS.map((p) => (
            <div key={p} className="rounded-md border border-slate-200 p-2 dark:border-slate-800">
              <dt className="text-sm font-semibold">{POSITION_LABEL[p]}</dt>
              <dd className="text-xs text-slate-600 dark:text-slate-300">{POSITION_MEANING[p]}</dd>
            </div>
          ))}
        </dl>
      </div>

      <GitSyncPanel state={gitSync} />

      <hr className="border-slate-200 dark:border-slate-800" />

      <SpecDocumentPanel
        content={specDocument}
        onContentChange={setSpecDocument}
        writeDisabledReason={writeDisabledReason}
      />

      <hr className="border-slate-200 dark:border-slate-800" />

      <SpecPlanPanel content={specDocument} writeDisabledReason={writeDisabledReason} />

      <hr className="border-slate-200 dark:border-slate-800" />

      <ThreeWayPanel gitSync={gitSync} specPresence={presence} writeDisabledReason={writeDisabledReason} />
    </div>
  );
}
