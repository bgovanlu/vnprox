// Shared capability gate for every zone wizard's final "Create draft" step
// — mirrors SdnZoneEditor.tsx's own `disabledReason` check exactly (same
// cluster-scoped capability lookup, since sdn.* ops are cluster-scoped, not
// per-node).
//
// T-607 fix: previously used `capsForNode(session, "").sdnWrite`, which
// internal/auth.BuildCapabilities (internal/auth/caps.go) only ever
// populates for the `""` caps-map key in the zero-nodes edge case — a real
// multi-node session's caps map has only per-node keys, so this silently
// evaluated to NO_CAPS and permanently disabled every zone wizard's
// "Create draft" button on any real cluster, root included. Every existing
// wizard unit test's mock session (wizardTestUtils.tsx) happens to include
// an artificial `""` entry that masked this; only caught by actually
// driving a wizard to completion against a real multi-node backend
// (web/e2e/user-guide-tasks.spec.ts). Fixed the same way the five
// SdnPage.tsx/editor dialogs' equivalent checks were: hasAnyCap resolves
// across every node in the session's caps map instead.
import { useSession } from "../../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../../changesets/capabilities";

export function useWizardCapability(): { denied: boolean; reason?: string } {
  const { data: session } = useSession();
  const canWrite = hasAnyCap(session, "sdnWrite");
  return { denied: !canWrite, reason: canWrite ? undefined : missingCapTooltip(session, "", "sdnWrite") };
}
