// Shared capability gate for every zone wizard's final "Create draft" step
// — mirrors SdnZoneEditor.tsx's own `disabledReason` check exactly (same
// cluster-scoped "" node capability lookup, since sdn.* ops are
// cluster-scoped, not per-node).
import { useSession } from "../../api/useSession";
import { capsForNode, missingCapTooltip } from "../../changesets/capabilities";

export function useWizardCapability(): { denied: boolean; reason?: string } {
  const { data: session } = useSession();
  const denied = !capsForNode(session, "").sdnWrite;
  return { denied, reason: denied ? missingCapTooltip(session, "", "sdnWrite") : undefined };
}
