// SPDX-License-Identifier: Apache-2.0

// T-3005: the apply-strategy picker on the review screen — how this apply
// fans out (T-2602) and whether a new error finding rolls it back inside the
// commit-confirm window (T-2603).
//
// Both features shipped complete in the backend and were reachable only with
// `curl`. Neither is defaulted on here: `mode: all` and auto-rollback-off are
// exactly what apply has always done, and this panel must not change what
// happens to an operator who ignores it.
import type { PlanStep } from "../api/types";
import { HelpAnchor } from "../help/HelpAnchor";
import {
  MAX_CANARY_HOLD_SEC,
  MIN_CANARY_HOLD_SEC,
  autoGateUnavailableReason,
  canaryEligibility,
  selectionError,
  type StrategySelection,
} from "./applyStrategy";

export interface ApplyStrategyPanelProps {
  /** The plan the apply will run — the persisted one if there is one, else
   * the review screen's client-side preview. Canary eligibility is a
   * property of the plan, not of the ops. */
  planSteps: readonly PlanStep[];
  selection: StrategySelection;
  onSelectionChange: (next: StrategySelection) => void;
  autoRollbackOnError: boolean;
  onAutoRollbackChange: (next: boolean) => void;
  /** The window the operator has chosen; the hold must be shorter than it. */
  confirmTimeoutSec: number;
}

const radioLabelClass = "flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200";

export function ApplyStrategyPanel({
  planSteps,
  selection,
  onSelectionChange,
  autoRollbackOnError,
  onAutoRollbackChange,
  confirmTimeoutSec,
}: ApplyStrategyPanelProps) {
  const eligibility = canaryEligibility(planSteps);
  const autoBlocked = autoGateUnavailableReason(planSteps);
  const error = selectionError(selection, eligibility, confirmTimeoutSec);

  function toggleNode(node: string): void {
    const next = selection.canaryNodes.includes(node)
      ? selection.canaryNodes.filter((n) => n !== node)
      : [...selection.canaryNodes, node];
    onSelectionChange({ ...selection, canaryNodes: next });
  }

  return (
    <section
      className="mt-3 rounded-md border border-slate-200 p-3 dark:border-slate-700"
      aria-label="Apply strategy"
      data-testid="apply-strategy-panel"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-medium text-fg-subtle">Apply strategy</h3>
        <HelpAnchor topic="canary-apply" />
      </div>

      <div className="mt-2 flex flex-col gap-2">
        <label className={radioLabelClass}>
          <input
            type="radio"
            name="apply-mode"
            className="mt-0.5"
            checked={selection.mode === "all"}
            onChange={() => {
              onSelectionChange({ ...selection, mode: "all" });
            }}
          />
          <span>
            <span className="font-medium">All nodes at once</span>
            <span className="block text-fg-subtle">
              Every affected node is applied together, then the commit-confirm window opens. This is what apply has
              always done.
            </span>
          </span>
        </label>

        <label className={radioLabelClass}>
          <input
            type="radio"
            name="apply-mode"
            className="mt-0.5"
            disabled={!eligibility.eligible}
            checked={selection.mode === "canary"}
            onChange={() => {
              onSelectionChange({ ...selection, mode: "canary" });
            }}
          />
          <span>
            <span className="font-medium">Canary — a few nodes first, then hold</span>
            <span className="block text-fg-subtle">
              Applies only the nodes you pick, pauses, and applies the rest once the hold is released. The
              commit-confirm window covers the whole sequence, so a stalled canary still rolls everything back.
            </span>
            {/* Never leave a disabled option unexplained: "canary is missing"
                and "canary does not apply to this change" must not look the
                same. */}
            {!eligibility.eligible && eligibility.reason !== undefined && (
              <span className="mt-1 block text-fg-subtle" data-testid="canary-ineligible-reason">
                Unavailable: {eligibility.reason}
              </span>
            )}
          </span>
        </label>
      </div>

      {selection.mode === "canary" && eligibility.eligible && (
        <div className="mt-3 flex flex-col gap-3 border-t border-slate-200 pt-3 dark:border-slate-700">
          <fieldset>
            <legend className="text-xs font-medium text-fg-subtle">Canary nodes</legend>
            <div className="mt-1 flex flex-wrap gap-3">
              {eligibility.nodes.map((node) => (
                <label key={node} className="flex items-center gap-1.5 text-xs text-slate-700 dark:text-slate-200">
                  <input
                    type="checkbox"
                    checked={selection.canaryNodes.includes(node)}
                    onChange={() => {
                      toggleNode(node);
                    }}
                  />
                  {node}
                </label>
              ))}
            </div>
          </fieldset>

          <label className="flex items-center gap-1.5 text-xs text-fg-subtle">
            Hold (s)
            <input
              type="number"
              aria-label="Canary hold seconds"
              min={MIN_CANARY_HOLD_SEC}
              max={MAX_CANARY_HOLD_SEC}
              value={selection.holdForSec}
              onChange={(e) => {
                onSelectionChange({ ...selection, holdForSec: Number(e.target.value) });
              }}
              className="w-16 rounded border border-slate-300 px-1.5 py-0.5 text-xs dark:border-slate-700 dark:bg-slate-800"
            />
          </label>

          <fieldset>
            <legend className="text-xs font-medium text-fg-subtle">Gate</legend>
            <div className="mt-1 flex flex-col gap-1">
              <label className={radioLabelClass}>
                <input
                  type="radio"
                  name="apply-gate"
                  className="mt-0.5"
                  checked={selection.gate === "manual"}
                  onChange={() => {
                    onSelectionChange({ ...selection, gate: "manual" });
                  }}
                />
                <span>
                  Manual — vnprox waits for you to continue after looking at the canary nodes.
                </span>
              </label>
              <label className={radioLabelClass}>
                <input
                  type="radio"
                  name="apply-gate"
                  className="mt-0.5"
                  disabled={autoBlocked !== undefined}
                  checked={selection.gate === "auto"}
                  onChange={() => {
                    onSelectionChange({ ...selection, gate: "auto" });
                  }}
                />
                <span>
                  Automatic — promotes at the hold deadline only if the canary nodes are healthy and no new
                  error-severity finding is attributable to them; otherwise it aborts.
                  {autoBlocked !== undefined && (
                    <span className="mt-1 block text-fg-subtle" data-testid="auto-gate-reason">
                      Unavailable: {autoBlocked}
                    </span>
                  )}
                  {/* The daemon-side half of the auto-gate check (is a canary
                      health checker wired?) is not visible from the browser,
                      so say so rather than promise it will work. */}
                  {autoBlocked === undefined && (
                    <span className="mt-1 block text-fg-subtle">
                      Needs a daemon with a canary health checker wired; if this one has none, the apply is refused
                      up front rather than promoted on silence.
                    </span>
                  )}
                </span>
              </label>
            </div>
          </fieldset>
        </div>
      )}

      <label className="mt-3 flex items-start gap-2 border-t border-slate-200 pt-3 text-xs text-slate-700 dark:border-slate-700 dark:text-slate-200">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={autoRollbackOnError}
          onChange={(e) => {
            onAutoRollbackChange(e.target.checked);
          }}
        />
        <span>
          <span className="font-medium">Roll back automatically on a new error finding</span>
          <span className="block text-fg-subtle">
            Off by default. This governs the commit-confirm <em>window</em>, not the fan-out: it does not change which
            nodes are applied or in what order. A new error-severity finding attributable to something this changeset
            touches, arriving inside the window, rolls it back immediately instead of waiting the window out. During a
            canary hold it aborts the sequence, restoring exactly the nodes already applied.
          </span>
          {/* The cluster default is a daemon-side setting with no read route,
              so the unticked state cannot honestly be reported as "off". Say
              what leaving it alone actually does instead of guessing. */}
          <span className="block text-fg-subtle">
            Ticking it arms the guard for this apply. Leaving it unticked asks for the cluster default, which is off
            unless an administrator set `auto_rollback_on_error`.
          </span>
        </span>
      </label>

      {error !== undefined && (
        <p className="mt-2 text-[11px] text-red-700 dark:text-red-300" role="alert" data-testid="apply-strategy-error">
          {error}
        </p>
      )}
    </section>
  );
}
