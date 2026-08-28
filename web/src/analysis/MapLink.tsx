// SPDX-License-Identifier: Apache-2.0

// A Ref rendered as a link into the topology inspector — the same
// select-then-navigate pair tools/MacFdbBrowser.tsx's owner badge uses, kept
// here as one component because three panels on this page need it.
//
// Deliberately a <button> and not an <a>: selecting an entity is topology
// store state, not a URL, so there is no href a middle-click could
// meaningfully open. The title attribute carries the full Ref so a truncated
// label never hides which entity is meant.
import { useNavigate } from "react-router-dom";
import { useTopologyStore } from "../topology/store";

export interface MapLinkProps {
  entityRef: string;
  /** Display text; the Ref itself when omitted. */
  label?: string;
}

export function MapLink({ entityRef, label }: MapLinkProps) {
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);
  return (
    <button
      type="button"
      title={`Open ${entityRef} in the topology inspector`}
      onClick={() => {
        select(entityRef);
        void navigate("/topology");
      }}
      className="rounded font-mono text-xs text-accent-700 underline decoration-dotted underline-offset-2 hover:decoration-solid dark:text-accent-300"
    >
      {label ?? entityRef}
    </button>
  );
}
