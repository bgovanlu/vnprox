// SPDX-License-Identifier: Apache-2.0

// TanStack Query mutations for the microsegmentation planner (T-1602's
// `POST /microseg/propose` + `POST /microseg/dry-run`). Both are modelled
// as mutations rather than queries because each is an explicit, on-demand,
// potentially-expensive synthesis a reviewer TRIGGERS (propose this guest's
// policy; now dry-run it) — never a background poll. They read-only compute;
// the write path is the ordinary ChangesetDrawer (useDrawerActions), not
// these calls.
import { useMutation } from "@tanstack/react-query";
import { dryRunMicroseg, proposeMicroseg } from "../api/microseg";
import type { MicrosegDryRunReport, MicrosegProposal } from "../api/types";

/** Mutation that computes the minimal covering-set policy for a guest. */
export function useMicrosegProposeMutation() {
  return useMutation<MicrosegProposal, Error, string>({
    mutationFn: (guestRef: string) => proposeMicroseg(guestRef),
  });
}

export interface DryRunVars {
  guestRef: string;
  heldOut: boolean;
}

/** Mutation that dry-runs the guest's proposal against the training corpus
 * (`heldOut: false`) or a trailing held-out window (`heldOut: true`). */
export function useMicrosegDryRunMutation() {
  return useMutation<MicrosegDryRunReport, Error, DryRunVars>({
    mutationFn: ({ guestRef, heldOut }: DryRunVars) => dryRunMicroseg(guestRef, heldOut),
  });
}
