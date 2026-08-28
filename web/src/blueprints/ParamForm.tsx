// SPDX-License-Identifier: Apache-2.0

// The blueprint instantiate param form: one input per ParamDef, inline
// validation (T-603 AC4: "bad CIDR/VID rejected at the form"), and an
// IPAM-aware "Suggest" button for addressSuggest-eligible params
// (GET /blueprints/{id}/suggest). See paramValidation.ts for the
// framework-free validation logic this component only wires up to state.
import { useState } from "react";
import type { FormEvent } from "react";
import type { BlueprintParamDef, BlueprintParamValue } from "../api/types";
import { Tooltip } from "../components/Tooltip";
import { defaultRawValue, validateParamForm } from "./paramValidation";
import { useSuggestAddressMutation } from "./queries";

export interface ParamFormProps {
  blueprintId: string;
  params: BlueprintParamDef[];
  /** Extra node-selector input, rendered above the params (comma/space
   * separated node names; empty means "every cluster node" per
   * docs/api.md's Blueprints section). */
  nodesValue: string;
  onNodesChange: (value: string) => void;
  onValidSubmit: (params: Record<string, BlueprintParamValue>) => void;
  submitLabel?: string;
  submitting?: boolean;
  /** When set, the submit button is disabled and shows this as a tooltip —
   * the capability-gating pattern every write affordance in this codebase
   * uses (changesets/capabilities.ts's missingCapTooltip). BlueprintsPage
   * computes this from the session's netWrite capability; ParamForm itself
   * stays capability-agnostic. */
  submitDisabledReason?: string;
}

function initialValues(params: BlueprintParamDef[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params) {
    out[p.name] = defaultRawValue(p);
  }
  return out;
}

export function ParamForm({
  blueprintId,
  params,
  nodesValue,
  onNodesChange,
  onValidSubmit,
  submitLabel = "Instantiate",
  submitting = false,
  submitDisabledReason,
}: ParamFormProps) {
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(params));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const suggestMutation = useSuggestAddressMutation();

  function setValue(name: string, v: string): void {
    setValues((prev) => ({ ...prev, [name]: v }));
  }

  function handleSubmit(e: FormEvent): void {
    e.preventDefault();
    const result = validateParamForm(params, values);
    setErrors(result.errors);
    if (result.params) {
      onValidSubmit(result.params);
    }
  }

  async function handleSuggest(def: BlueprintParamDef): Promise<void> {
    const result = await suggestMutation.mutateAsync({ id: blueprintId, param: def.name });
    setValue(def.name, result.address);
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} data-testid="blueprint-param-form">
      <div>
        <label htmlFor="bp-nodes" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
          Target nodes
        </label>
        <input
          id="bp-nodes"
          type="text"
          className="mt-1 w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          placeholder="every cluster node"
          value={nodesValue}
          onChange={(e) => {
            onNodesChange(e.target.value);
          }}
        />
      </div>

      {params.map((def) => (
        <div key={def.name}>
          <label htmlFor={`bp-param-${def.name}`} className="block text-sm font-medium text-slate-700 dark:text-slate-300">
            {def.label ?? def.name}
            {def.required ? <span aria-hidden className="text-red-600"> *</span> : null}
          </label>
          {def.description ? <p className="text-xs text-slate-500 dark:text-slate-400">{def.description}</p> : null}
          <div className="mt-1 flex gap-2">
            <input
              id={`bp-param-${def.name}`}
              type="text"
              className="w-full rounded-md border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
              value={values[def.name] ?? ""}
              onChange={(e) => {
                setValue(def.name, e.target.value);
              }}
              aria-invalid={errors[def.name] !== undefined}
              aria-describedby={errors[def.name] ? `bp-param-${def.name}-error` : undefined}
            />
            {def.addressSuggest ? (
              <button
                type="button"
                className="shrink-0 rounded-md border border-slate-300 px-2 py-1 text-xs font-medium text-slate-600 hover:bg-slate-100 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-800"
                onClick={() => {
                  void handleSuggest(def);
                }}
                disabled={suggestMutation.isPending}
              >
                Suggest
              </button>
            ) : null}
          </div>
          {errors[def.name] ? (
            <p id={`bp-param-${def.name}-error`} className="mt-1 text-xs text-red-600 dark:text-red-400">
              {errors[def.name]}
            </p>
          ) : null}
        </div>
      ))}

      <Tooltip content={submitDisabledReason}>
        <span>
          <button
            type="submit"
            className="mt-2 rounded-md bg-accent-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-accent-700 disabled:opacity-50"
            disabled={submitting || submitDisabledReason !== undefined}
          >
            {submitting ? "Working…" : submitLabel}
          </button>
        </span>
      </Tooltip>
    </form>
  );
}
