// The first-login onboarding walkthrough (docs/user-guide.md §1; T-605
// AC1/AC2). Deliberately built on the same non-modal-overlay pattern as
// ChangesetDrawer.tsx (see that file's doc comment) rather than a
// full-screen modal: the task card requires it "never block navigation",
// so this renders as a plain always-mounted floating panel the user can
// keep open while clicking around the rest of the app, not a dialog that
// traps focus. <OnboardingWalkthrough/> mounts once in AppShell, exactly
// like <ChangesetDrawer/>.
//
// Structural split mirrors changesets/drawerMachine.ts +
// changesets/ChangesetDrawer.tsx: onboardingMachine.ts (and this task's
// other onboarding/*.ts pure modules) own every transition/derivation as
// framework-free, Vitest-tested functions; this component is a thin
// renderer of whatever those functions and the per-step data queries
// return.
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useSession } from "../api/useSession";
import { Button } from "../components/Button";
import { Tooltip } from "../components/Tooltip";
import { useToast } from "../components/Toast";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useDriftQuery } from "../drift/queries";
import { useTopologyQuery } from "../topology/queries";
import type { OnboardingProgress, OnboardingStep, Severity } from "../api/types";
import { ALL_LAYERS } from "../api/types";
import { summarizeFound } from "./foundSummary";
import {
  ONBOARDING_STEPS,
  completeStep,
  dismissOnboarding,
  resumeOnboarding,
  shouldShowPanel,
  shouldShowReopenPill,
  skipStep,
} from "./onboardingMachine";
import { draftFromSuggestion, draftToRequestNodes, isRefSelected, selectedCount, toggleRef, type ProtectedDraft } from "./protectedDraft";
import {
  useLldpInstallMutation,
  useLldpQuery,
  useOnboardingProgressQuery,
  useProtectedInterfacesQuery,
  useProtectedInterfacesSuggestQuery,
  useSaveOnboardingProgressMutation,
  useSaveProtectedInterfacesMutation,
} from "./queries";

const LAYER_LABEL: Record<(typeof ALL_LAYERS)[number], string> = {
  phys: "Physical",
  l2: "L2 (bonds/bridges/VLANs)",
  sdn: "SDN",
  guest: "Guests",
};

const STEP_TITLE: Record<OnboardingStep, string> = {
  "found-summary": "What we found",
  protected: "Protected interfaces",
  lldp: "Physical discovery",
  health: "Health findings",
  done: "Done",
};

function stepNumber(step: OnboardingStep): number {
  const idx = ONBOARDING_STEPS.indexOf(step);
  return idx === -1 ? ONBOARDING_STEPS.length : idx + 1;
}

interface StepProps {
  onComplete: () => void;
  onSkip: () => void;
}

/** Step 1: a read-only summary of GET /topology — "Nothing was changed;
 * vnprox only read" (docs/user-guide.md §1.1). No write affordance, so no
 * capability gating needed. */
function FoundSummaryStep({ onComplete }: StepProps) {
  const { data: topology, isLoading } = useTopologyQuery();
  const summary = summarizeFound(topology);

  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-slate-600 dark:text-slate-300">
        Your cluster&apos;s network, drawn. Nothing was changed; vnprox only read.
      </p>
      {isLoading ? (
        <p className="text-slate-400">Scanning the cluster…</p>
      ) : (
        <>
          <p>
            <span className="font-semibold">{summary.clusterNodes.length}</span> node(s):{" "}
            {summary.clusterNodes.join(", ") || "none detected"}
          </p>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1">
            {ALL_LAYERS.map((layer) => (
              <div className="contents" key={layer}>
                <dt className="text-slate-500 dark:text-slate-400">{LAYER_LABEL[layer]}</dt>
                <dd className="text-right font-medium">{summary.byLayer[layer]}</dd>
              </div>
            ))}
          </dl>
          <p className="text-xs text-slate-400">
            {summary.totalEntities} entities, {summary.totalEdges} connections total.
          </p>
        </>
      )}
      <Button size="sm" variant="primary" onClick={onComplete}>
        Continue
      </Button>
    </div>
  );
}

/** Step 2: confirm/correct the detected management + corosync interfaces
 * (docs/user-guide.md §1.2). Pre-fills from GET /protected-interfaces/suggest
 * (unioned with anything already confirmed), lets the user uncheck a
 * wrongly-detected ref, and PUTs the result on confirm. The write (PUT
 * /protected-interfaces) is a cluster-wide call, not scoped to one node —
 * gated on hasAnyCap(session, "netWrite") per capabilities.ts's doc comment
 * for that helper, disabled-with-tooltip rather than hidden (a read-only
 * user can still see and skip this step). */
function ProtectedStep({ onComplete, onSkip }: StepProps) {
  const { data: session } = useSession();
  const { data: suggestion, isLoading: suggestLoading } = useProtectedInterfacesSuggestQuery();
  const { data: existing } = useProtectedInterfacesQuery();
  const saveMutation = useSaveProtectedInterfacesMutation();
  const { toast } = useToast();
  // `candidates` is the fixed, never-mutated set of refs the user can
  // toggle (the union of suggested + previously-confirmed, computed once);
  // `draft` is the currently-*selected* subset of it, which toggleRef
  // shrinks/grows. Kept as two separate pieces of state (rather than one)
  // so unchecking a ref removes it from the PUT payload without also
  // removing its checkbox row from view — a user must be able to re-check
  // something they unchecked by mistake.
  const [candidates, setCandidates] = useState<ProtectedDraft>({});
  const [draft, setDraft] = useState<ProtectedDraft>({});
  const seeded = useRef(false);

  useEffect(() => {
    if (seeded.current || !suggestion) return;
    seeded.current = true;
    const initial = draftFromSuggestion(suggestion, existing);
    setCandidates(initial);
    setDraft(initial);
  }, [suggestion, existing]);

  const nodes = Object.keys(candidates);
  const canWrite = hasAnyCap(session, "netWrite");
  const disabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  async function handleConfirm(): Promise<void> {
    try {
      await saveMutation.mutateAsync({ nodes: draftToRequestNodes(draft) });
      toast({ title: "Protected interfaces saved", variant: "success" });
      onComplete();
    } catch {
      toast({ title: "Could not save protected interfaces", variant: "error" });
    }
  }

  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-slate-600 dark:text-slate-300">
        vnprox detected which interfaces carry each node&apos;s management IP and corosync traffic. Confirm these;
        vnprox will refuse changes that would cut them off.
      </p>
      {suggestLoading && nodes.length === 0 ? (
        <p className="text-slate-400">Detecting…</p>
      ) : nodes.length === 0 ? (
        <p className="text-slate-400">No management/corosync interfaces were detected to confirm.</p>
      ) : (
        <ul className="max-h-48 space-y-2 overflow-y-auto">
          {nodes.map((node) => (
            <li key={node}>
              <p className="text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">{node}</p>
              <ul className="ml-1">
                {(candidates[node] ?? []).map((ref) => (
                  <li key={ref}>
                    <label className="flex items-center gap-2 py-0.5">
                      <input
                        type="checkbox"
                        checked={isRefSelected(draft, node, ref)}
                        disabled={!canWrite}
                        onChange={() => {
                          setDraft((d) => toggleRef(d, node, ref));
                        }}
                      />
                      <span className="font-mono text-xs">{ref}</span>
                    </label>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-slate-400">{selectedCount(draft)} interface(s) selected.</p>
      <div className="flex gap-2">
        <Tooltip content={disabledReason}>
          <span>
            <Button size="sm" variant="primary" disabled={!canWrite || saveMutation.isPending} onClick={() => void handleConfirm()}>
              Confirm protected interfaces
            </Button>
          </span>
        </Tooltip>
        <Button size="sm" variant="ghost" onClick={onSkip}>
          Skip
        </Button>
      </div>
    </div>
  );
}

/** Step 3: offer to enable lldpd if it isn't already reporting neighbors
 * (docs/user-guide.md §1.3). POST /lldp/install is a cluster-wide fan-out
 * call, gated the same way as ProtectedStep's confirm. */
function LldpStep({ onComplete, onSkip }: StepProps) {
  const { data: session } = useSession();
  const { data: lldp, isLoading } = useLldpQuery();
  const installMutation = useLldpInstallMutation();
  const { toast } = useToast();

  const alreadyRunning = (lldp?.items.length ?? 0) > 0;
  const canWrite = hasAnyCap(session, "netWrite");
  const disabledReason = canWrite ? undefined : missingCapTooltip(session, "", "netWrite");

  async function handleInstall(): Promise<void> {
    try {
      const res = await installMutation.mutateAsync();
      const failed = res.results.filter((r) => !r.ok);
      if (failed.length > 0) {
        toast({
          title: "LLDP install finished with errors",
          description: failed.map((f) => `${f.node}: ${f.error ?? "unknown error"}`).join("; "),
          variant: "error",
        });
      } else {
        toast({ title: "lldpd enabled cluster-wide", variant: "success" });
      }
      onComplete();
    } catch {
      toast({ title: "Could not install lldpd", variant: "error" });
    }
  }

  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-slate-600 dark:text-slate-300">
        If <code>lldpd</code> isn&apos;t running, vnprox offers to enable it so the map can show real switch names and
        ports.
      </p>
      {isLoading ? (
        <p className="text-slate-400">Checking for LLDP neighbors…</p>
      ) : alreadyRunning ? (
        <p>
          <span className="font-semibold">{lldp?.items.length}</span> LLDP neighbor(s) already reporting — nothing to
          enable.
        </p>
      ) : (
        <p className="text-slate-400">No LLDP neighbors seen yet. lldpd may not be running on this cluster.</p>
      )}
      <div className="flex gap-2">
        {alreadyRunning ? (
          <Button size="sm" variant="primary" onClick={onComplete}>
            Continue
          </Button>
        ) : (
          <Tooltip content={disabledReason}>
            <span>
              <Button size="sm" variant="primary" disabled={!canWrite || installMutation.isPending} onClick={() => void handleInstall()}>
                Enable LLDP discovery
              </Button>
            </span>
          </Tooltip>
        )}
        <Button size="sm" variant="ghost" onClick={onSkip}>
          Skip
        </Button>
      </div>
    </div>
  );
}

const SEVERITY_ORDER: Severity[] = ["error", "warning", "info"];

/** Step 4: a thin, shape-agnostic summary of GET /drift — a count + severity
 * breakdown only, linking to the Tools page's existing DriftFindingsPanel
 * rather than reimplementing any part of the findings list (T-602 is
 * reshaping internal/drift's finding shape concurrently — see this
 * component's file-level doc comment). */
function HealthStep({ onComplete }: StepProps) {
  const { data: findings, isLoading } = useDriftQuery();
  const counts: Record<Severity, number> = { error: 0, warning: 0, info: 0 };
  for (const f of findings ?? []) {
    counts[f.severity] += 1;
  }
  const total = findings?.length ?? 0;

  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-slate-600 dark:text-slate-300">
        Anything inconsistent vnprox noticed (MTU mismatches, half-applied configs, drift between nodes).
      </p>
      {isLoading ? (
        <p className="text-slate-400">Checking for drift…</p>
      ) : (
        <p>
          <span className="font-semibold">{total}</span> finding(s) (
          {SEVERITY_ORDER.map((sev) => `${String(counts[sev])} ${sev}${counts[sev] === 1 ? "" : "s"}`).join(", ")})
        </p>
      )}
      <Link to="/tools" className="text-xs text-accent-700 underline dark:text-accent-400">
        Open Tools → Drift findings
      </Link>
      <Button size="sm" variant="primary" onClick={onComplete}>
        Finish
      </Button>
    </div>
  );
}

function persistAndApply(
  progress: OnboardingProgress,
  transform: (p: OnboardingProgress) => OnboardingProgress,
  save: (p: OnboardingProgress) => void,
): void {
  save(transform(progress));
}

/** Mount once, near the app root (AppShell), so it persists across route
 * navigation like <ChangesetDrawer/> — the walkthrough is not scoped to
 * any one page (a user can keep it open while clicking around Topology,
 * Guests, etc., per the task card's "must never block navigation"). */
export function OnboardingWalkthrough() {
  const { data: progress } = useOnboardingProgressQuery();
  const saveMutation = useSaveOnboardingProgressMutation();
  const { toast } = useToast();

  if (!progress) return null;

  function save(next: OnboardingProgress): void {
    saveMutation.mutate(next, {
      onError: () => {
        toast({ title: "Could not save onboarding progress", description: "Your place may not be remembered on reload.", variant: "error" });
      },
    });
  }

  const handleComplete = (): void => {
    persistAndApply(progress, completeStep, save);
  };
  const handleSkip = (): void => {
    persistAndApply(progress, skipStep, save);
  };
  const handleDismiss = (): void => {
    persistAndApply(progress, (p) => dismissOnboarding(p, Date.now()), save);
  };
  const handleResume = (): void => {
    persistAndApply(progress, resumeOnboarding, save);
  };

  if (shouldShowReopenPill(progress)) {
    return (
      <div className="shrink-0 border-b border-slate-200 bg-white px-4 py-1.5 dark:border-slate-800 dark:bg-slate-950">
        <button
          type="button"
          onClick={handleResume}
          className="rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-900 dark:hover:bg-slate-800"
        >
          Resume setup walkthrough ({stepNumber(progress.currentStep)}/{ONBOARDING_STEPS.length})
        </button>
      </div>
    );
  }

  if (!shouldShowPanel(progress)) {
    return null;
  }

  return (
    <div
      role="region"
      aria-label="Onboarding walkthrough"
      // Normal document flow (a banner between TopBar and <main>, pushing
      // content down), not a fixed floating overlay — see AppShell.tsx's
      // doc comment on why: every fixed corner tried collided with some
      // page's own controls (React Flow's bottom-left Controls, the
      // topology toolbar's top-row New/Search buttons, ChangesetDrawer's
      // bottom-right corner).
      className="flex w-full max-w-md shrink-0 flex-col overflow-hidden border-b border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-950"
    >
      <div className="flex items-center justify-between gap-2 bg-slate-50 px-3 py-2 dark:bg-slate-800/60">
        <span className="flex items-baseline gap-1.5 text-sm font-medium">
          <span className="text-xs font-normal text-slate-400">
            {stepNumber(progress.currentStep)}/{ONBOARDING_STEPS.length}
          </span>
          <span>{STEP_TITLE[progress.currentStep]}</span>
        </span>
        <button
          type="button"
          aria-label="Minimize onboarding walkthrough"
          onClick={handleDismiss}
          className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
        >
          ▾
        </button>
      </div>
      <div className="p-3">
        {progress.currentStep === "found-summary" && <FoundSummaryStep onComplete={handleComplete} onSkip={handleSkip} />}
        {progress.currentStep === "protected" && <ProtectedStep onComplete={handleComplete} onSkip={handleSkip} />}
        {progress.currentStep === "lldp" && <LldpStep onComplete={handleComplete} onSkip={handleSkip} />}
        {progress.currentStep === "health" && <HealthStep onComplete={handleComplete} onSkip={handleSkip} />}
      </div>
    </div>
  );
}
