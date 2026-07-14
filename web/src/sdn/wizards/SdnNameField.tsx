// Shared zone/vnet name input for the guided SDN wizards (issue #3): renders
// a name field with inline, as-you-type feedback — a hard red error for a
// charset PVE rejects (sdnNameError) and a soft amber warning for an
// over-long id (sdnNameWarning). The wizard step's own `isValid` still
// gates Next; import sdnNameError there to fold this field's hard error in.
import { Field, inputClass } from "../../changesets/editors/EditorDialog";
import { sdnNameError, sdnNameWarning } from "./validation";

export interface SdnNameFieldProps {
  label: string;
  help: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}

export function SdnNameField({ label, help, value, onChange, placeholder }: SdnNameFieldProps) {
  const err = sdnNameError(value);
  const warn = err ? undefined : sdnNameWarning(value);
  return (
    <div>
      <Field label={label} help={help} errors={err ? [err] : undefined}>
        <input
          className={inputClass}
          value={value}
          onChange={(e) => {
            onChange(e.target.value);
          }}
          placeholder={placeholder}
        />
      </Field>
      {warn && (
        <p role="status" className="mt-0.5 text-[11px] text-amber-600 dark:text-amber-400">
          {warn}
        </p>
      )}
    </div>
  );
}
