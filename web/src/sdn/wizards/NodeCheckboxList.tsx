// SPDX-License-Identifier: Apache-2.0

// Shared "which cluster nodes" multi-select every wizard's zone step
// needs — a plain checkbox list rather than a comma-separated text input
// (SdnZoneEditor.tsx's own `parseNodes` pattern), since the wizard already
// knows the real node list (useClusterNodes) and picking from it avoids
// typos that would otherwise only surface as a referential validation
// error after drafting.
//
// A <fieldset>/<legend> of its own, not wrapped in EditorDialog.tsx's
// <Field> (which is a single <label> element): nesting a checkbox's own
// implicit <label for=[control]> inside another wrapping <label> is
// invalid HTML, and in practice broke accessible-name computation for
// every checkbox (the outer label's implicit "first labelable descendant"
// association collided with each checkbox's own inner label) — every
// checkbox ended up effectively unnamed to assistive tech and to
// `getByRole(..., {name})` queries alike. A <fieldset> is also the
// semantically correct grouping for a set of checkboxes in the first
// place, not a workaround.
import type { ReactNode } from "react";
import { FieldHelp } from "../../changesets/editors/EditorDialog";

export interface NodeCheckboxListProps {
  label: string;
  help?: ReactNode;
  allNodes: string[];
  selected: string[];
  onChange: (nodes: string[]) => void;
}

export function NodeCheckboxList({ label, help, allNodes, selected, onChange }: NodeCheckboxListProps) {
  const selectedSet = new Set(selected);

  function toggle(node: string): void {
    if (selectedSet.has(node)) {
      onChange(selected.filter((n) => n !== node));
    } else {
      onChange([...selected, node]);
    }
  }

  return (
    <fieldset className="block border-0 p-0 m-0">
      <legend className="mb-1 block text-xs font-medium text-fg-muted">{label}</legend>
      {allNodes.length === 0 ? (
        <p className="text-xs text-fg-muted">No cluster nodes discovered yet.</p>
      ) : (
        <div className="flex flex-wrap gap-3">
          {allNodes.map((node) => (
            <label key={node} className="flex items-center gap-1.5 text-sm">
              <input type="checkbox" checked={selectedSet.has(node)} onChange={() => { toggle(node); }} />
              {node}
            </label>
          ))}
        </div>
      )}
      {help && <FieldHelp>{help}</FieldHelp>}
    </fieldset>
  );
}
