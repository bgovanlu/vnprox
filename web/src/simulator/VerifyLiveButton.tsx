// SPDX-License-Identifier: Apache-2.0

// T-806's "Verify live" action: gated on the current source endpoint
// resolving to a qemu guest with a reachable guest agent (verifyEligibility.ts's
// pure gating logic backs this), calling POST /simulate/verify on click.
// Rendered by SimulatorPage.tsx only once a completed simulate result
// exists — this component itself doesn't know or care about the simulated
// result's own content, only the request tuple needed to verify it.
import { Button } from "../components/Button";
import type { SimEndpointSpec, SimulateRequest, VerifyResult } from "../api/types";
import { useVerifyEligibilityQuery, useVerifyMutation } from "./queries";
import { verifyLiveGate } from "./verifyEligibility";

export interface VerifyLiveButtonProps {
  src: SimEndpointSpec | undefined;
  request: SimulateRequest;
  onResult: (result: VerifyResult) => void;
}

export function VerifyLiveButton({ src, request, onResult }: VerifyLiveButtonProps) {
  const eligibility = useVerifyEligibilityQuery(src);
  const gate = verifyLiveGate(src, eligibility.data, eligibility.isLoading, eligibility.isError);
  const verify = useVerifyMutation();

  function handleClick(): void {
    verify.mutate(request, { onSuccess: (data) => { onResult(data); } });
  }

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          variant="secondary"
          disabled={!gate.enabled || verify.isPending}
          aria-describedby={!gate.enabled ? "verify-live-reason" : undefined}
          onClick={handleClick}
        >
          {verify.isPending ? "Verifying…" : "Verify live"}
        </Button>
        {verify.isError && (
          <span className="text-xs text-red-600 dark:text-red-400">
            {verify.error instanceof Error ? verify.error.message : "Could not run the live probe."}
          </span>
        )}
      </div>
      {!gate.enabled && gate.reason && (
        <p id="verify-live-reason" className="text-xs text-fg-muted">
          {gate.reason}
        </p>
      )}
    </div>
  );
}
