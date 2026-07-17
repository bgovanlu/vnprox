// T-806's "Verify live" gating logic (docs/features/firewall.md §5:
// "enabled only for a guest-nic src resolved to a qemu guest with a
// reachable guest agent, with plain-English grey-out copy otherwise").
// Deliberately framework-free (no React) so the copy/enablement decision
// is exhaustively Vitest-able without rendering anything — the same
// "pure logic, separate from the component" split pathHighlight.ts and
// urlState.ts already establish in this folder.
import type { SimEndpointSpec, VerifyEligibility } from "../api/types";

export interface VerifyLiveGate {
  enabled: boolean;
  /** Plain-English reason shown next to the greyed-out button — always
   * present when `enabled` is false, per T-403's non-expert-copy bar
   * ("verify live requires a QEMU guest source with the guest agent
   * running" style, never a raw error code/message). Undefined when
   * enabled (nothing to explain). */
  reason?: string;
}

const NOT_GUEST_NIC_REASON =
  "Verify live requires a QEMU guest source with the guest agent running — pick a guest NIC as the source to enable it.";
const NOT_QEMU_REASON =
  "Verify live requires a QEMU guest source with the guest agent running — this source isn't a QEMU guest.";
const AGENT_UNREACHABLE_REASON =
  "Verify live requires a QEMU guest source with the guest agent running — no guest agent was detected on this guest.";
const CHECKING_REASON = "Checking whether this guest's agent is reachable…";
const UNKNOWN_REASON = "Verify live requires a QEMU guest source with the guest agent running.";

/**
 * Computes the "Verify live" button's enabled/disabled state and grey-out
 * copy from the current source endpoint and (once fetched) its eligibility
 * check result.
 *
 * `src` is the simulator's currently-picked source endpoint — undefined
 * (nothing picked yet), `kind: "ip"`/`"external"` (can't host a probe at
 * all — internal/api's own `POST /simulate/verify` 400s these, so the
 * frontend never even attempts the eligibility round trip for them, see
 * queries.ts's `useVerifyEligibilityQuery`), or `kind: "guest-nic"`.
 *
 * `eligibility`/`isLoading`/`isError` mirror a TanStack Query result for
 * `GET /simulate/verify/eligibility` — only fetched for a guest-nic src.
 */
export function verifyLiveGate(
  src: SimEndpointSpec | undefined,
  eligibility: VerifyEligibility | undefined,
  isLoading: boolean,
  isError: boolean,
): VerifyLiveGate {
  if (src?.kind !== "guest-nic") {
    return { enabled: false, reason: NOT_GUEST_NIC_REASON };
  }
  if (isLoading) {
    return { enabled: false, reason: CHECKING_REASON };
  }
  if (isError || !eligibility) {
    return { enabled: false, reason: UNKNOWN_REASON };
  }
  if (eligibility.eligible) {
    return { enabled: true };
  }
  switch (eligibility.reason) {
    case "not-qemu":
      return { enabled: false, reason: NOT_QEMU_REASON };
    case "agent-unreachable":
      return { enabled: false, reason: AGENT_UNREACHABLE_REASON };
    default:
      return { enabled: false, reason: UNKNOWN_REASON };
  }
}
