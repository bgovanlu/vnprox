// T-2805 — "who else is looking at this", in the changeset drawer.
//
// Mounting this component is what declares presence: the WS bridge below
// subscribes to `presence:<scope>` for as long as it is rendered, and
// unmounting (or the connection dropping) is what retracts it.
//
// It says nothing at all when nobody else is here — an empty presence
// indicator is noise on every screen for the single-operator case, which is
// most of them.
import { usePresenceQuery, usePresenceWsBridge } from "./presenceQueries";
import { othersPresent, presenceSentence } from "./lockWarning";
import { changesetScope } from "../api/locks";

export interface PresenceIndicatorProps {
  changesetId: string;
  /** The signed-in user, so the sentence says "N *others*" — the server
   * counts every viewer including this one. Omit it (an unknown identity)
   * and nobody is subtracted, which over-counts by one rather than
   * under-counting: claiming a colleague is absent would be the worse
   * error for a feature whose whole job is to say someone else is here. */
  currentUser?: string;
}

export function PresenceIndicator({ changesetId, currentUser }: PresenceIndicatorProps) {
  const scope = changesetScope(changesetId);
  usePresenceWsBridge(scope);
  const { data } = usePresenceQuery(scope);

  const state = data?.scopes.find((s) => s.scope === scope);
  if (!state) return null;

  const viewers = state.viewers ?? [];
  const includesSelf = currentUser !== undefined && viewers.some((v) => v.user === currentUser);
  // With identities withheld, `viewers` is empty and we cannot tell whether
  // this session is among the count — so assume it is (the common case: this
  // component only renders for someone who is, by definition, looking).
  const others = othersPresent(state.count, viewers.length === 0 ? true : includesSelf);
  const names = viewers.filter((v) => v.user !== currentUser).map((v) => v.user);
  const sentence = presenceSentence(others, names);
  if (sentence === "") return null;

  return (
    <p
      data-testid="presence-indicator"
      className="text-xs text-slate-600 dark:text-slate-400"
      // A status, not an alert: presence changing must never steal focus or
      // interrupt a screen reader mid-sentence.
      role="status"
      aria-live="polite"
    >
      {sentence}
    </p>
  );
}
