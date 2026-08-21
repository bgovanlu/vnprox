// Phase 36: one place that turns a producer-declared `Remediation` into a
// button, for every surface that renders findings — the unified findings
// stream, the topology banners, and whatever comes next.
//
// The rule this file exists to enforce: **no component decides which button
// to draw by looking at a finding's `check`.** That is how it worked before
// (`f.check === "mgmt_single_path"` in FindingsStreamPanel.tsx chose the
// redundancy wizard, `f.check === "sim_divergence"` chose the simulator
// deep link), and it meant every new remedy had to be added to every
// surface that displays findings, with nothing to catch the one that was
// missed — the finding still rendered, just without its button.
//
// So resolution keys off `remedy.action`, a stable wire identifier, and an
// action this build does not recognise resolves to `undefined`: no button,
// no crash, no guess. That is deliberate and is what lets a newer daemon
// introduce a remedy an older SPA simply doesn't offer yet.
import type { Remediation } from "../api/types";
import { mgmtStrings } from "../mgmt/strings";

/** The button copy for each known action.
 *
 * The label lives here, not in the daemon's `remedy.label`, for two
 * reasons. It is user-visible copy, and this repo keeps user-visible copy
 * in the frontend where `i18nCoverage.test.ts` can see it — a string
 * shipped from Go is invisible to that check. And it keeps the existing
 * wording identical: `mgmt.redundancy` reuses `mgmtStrings.launch.button`,
 * the exact label that affordance has always had, rather than a second
 * spelling of the same idea introduced by moving where it is decided.
 *
 * `remedy.label` remains the fallback, so a daemon offering an action this
 * build has copy for uses ours, and one offering an action we merely know
 * how to *run* still renders something sensible. */
const ACTION_LABELS: Readonly<Record<string, string>> = {
  "mgmt.redundancy": mgmtStrings.launch.button,
  navigate: "View in simulator",
};

function labelFor(remedy: Remediation): string {
  return ACTION_LABELS[remedy.action] ?? remedy.label;
}

/** What a resolved remedy needs from the surface rendering it. Kept to the
 * capabilities and navigation a handler might need, so this module stays
 * free of store/router imports and remains testable as a pure function. */
export interface RemediationContext {
  /** Whether this session may perform mutating network operations. An
   * `operational` remedy resolves to `undefined` without it — a disabled
   * button that explains nothing is worse than no button, and a read-only
   * operator is better served by the finding's own text. */
  netWrite: boolean;
  /** In-app navigation (react-router's navigate, wrapped by the caller). */
  navigate: (to: string) => void;
  /** Opens T-703's management-redundancy wizard for a node. */
  openMgmtWizard?: (opts: { node: string }) => void;
  /** Runs an `operational` remedy after the surface has confirmed it with
   * the operator. The surface owns the confirmation dialog — this module
   * deliberately owns no UI — but it must not call this without one.
   * Omitted = no operational remedy resolves, same as lacking netWrite. */
  runOperational?: (remedy: Remediation) => void;
}

/** A remedy resolved into something a list or banner can render. */
export interface ResolvedRemediation {
  label: string;
  /** True when firing this mutates something and the surface must confirm
   * before calling `onClick` (Phase 36's Tier 2 ceremony). */
  confirms: boolean;
  onClick: () => void;
}

/**
 * Resolves a producer-declared remedy for this session, or `undefined` when
 * there is nothing safe and meaningful to offer: no remedy, an unrecognised
 * action, a missing required parameter, an operational remedy without the
 * capability or the runner to perform it.
 *
 * Returning `undefined` rather than a disabled button is the consistent
 * answer to all of those, because they are the same answer from the
 * operator's point of view: this session cannot do this from here.
 */
export function remediationAction(
  remedy: Remediation | undefined,
  ctx: RemediationContext,
): ResolvedRemediation | undefined {
  if (!remedy) return undefined;

  if (remedy.kind === "operational") {
    // Gate on capability AND on the surface having supplied a runner. Both
    // are "cannot perform"; neither is a reason to draw something.
    if (!ctx.netWrite || !ctx.runOperational) return undefined;
    const run = ctx.runOperational;
    // Every known operational action is dispatched by the surface's runner
    // rather than resolved to a specific call here: the actions differ in
    // their request shape and their result rendering (per-node results for
    // an lldpd install, a single error string for a service start), and
    // that belongs with the mutation, not in a pure resolver.
    if (!OPERATIONAL_ACTIONS.has(remedy.action)) return undefined;
    return {
      label: labelFor(remedy),
      confirms: true,
      onClick: () => {
        run(remedy);
      },
    };
  }

  switch (remedy.action) {
    case "mgmt.redundancy": {
      const node = remedy.params?.node;
      if (node === undefined || node === "" || !ctx.openMgmtWizard) return undefined;
      const open = ctx.openMgmtWizard;
      return {
        label: labelFor(remedy),
        confirms: false,
        onClick: () => {
          open({ node });
        },
      };
    }
    case "navigate": {
      const to = remedy.params?.to;
      if (to === undefined || to === "") return undefined;
      return {
        label: labelFor(remedy),
        confirms: false,
        onClick: () => {
          ctx.navigate(to);
        },
      };
    }
    default:
      // An action this build has never heard of. See the header comment:
      // silence is the designed behaviour, not an oversight.
      return undefined;
  }
}

/** The `operational` actions this build knows how to run. Listed rather
 * than accepted wholesale so that a daemon offering an operational remedy
 * this SPA has no runner for renders nothing, instead of a button that
 * posts to a route the client cannot shape a request for. */
const OPERATIONAL_ACTIONS: ReadonlySet<string> = new Set<string>([
  "lldp.install",
  "collector.refresh",
  "service.start",
]);

/** Whether `action` is one this build can run — exported for the surfaces
 * that build their own confirmation copy per action. */
export function isKnownOperationalAction(action: string): boolean {
  return OPERATIONAL_ACTIONS.has(action);
}
