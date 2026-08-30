// SPDX-License-Identifier: Apache-2.0

// T-3002: the compliance profiles — `GET /compliance` and
// `GET /compliance/{profile}`.
//
// T-2706 built a reporter that answers `unmapped` honestly and shipped it with
// no reader. The whole value of the four-value vocabulary is that three of
// those values are not a pass, so this panel's job is to make the difference
// visible rather than tidy it away: every control renders its own status word
// and what that word means, and `unmapped` is never hidden and never counted
// as passing (see complianceStatus.ts, which owns the single `isPassing`
// predicate).
import { useState } from "react";
import { HelpAnchor } from "../help/HelpAnchor";
import { ApiError } from "../api/client";
import { useComplianceProfilesQuery, useComplianceReportQuery } from "./queries";
import {
  CONTROL_STATUS_CLASS,
  CONTROL_STATUS_LABEL,
  CONTROL_STATUS_MEANING,
  KNOWN_CONTROL_STATUSES,
  classifyControls,
  countByStatus,
  summaryDisagrees,
} from "./complianceStatus";

export function CompliancePanel() {
  const profilesQuery = useComplianceProfilesQuery();
  const [selected, setSelected] = useState<string | undefined>(undefined);
  const profiles = profilesQuery.data ?? [];
  const activeProfile = selected ?? profiles[0]?.id;
  const reportQuery = useComplianceReportQuery(activeProfile);

  const controls = classifyControls(reportQuery.data);
  const counts = countByStatus(controls);
  const disagrees = summaryDisagrees(reportQuery.data, counts);

  return (
    <section aria-label="Compliance profiles" data-testid="compliance-panel" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold">Compliance profiles</h2>
        <HelpAnchor topic="compliance-panel" />
      </div>

      {profilesQuery.isLoading && (
        <p className="text-sm text-fg-muted">Reading the installed profiles…</p>
      )}
      {profilesQuery.error !== null && (
        <p className="text-sm text-fg-body" role="status">
          The profile list could not be read, so which profiles are installed is unknown. The daemon said:{" "}
          {profilesQuery.error instanceof Error ? profilesQuery.error.message : "the read failed"}
        </p>
      )}
      {profilesQuery.data !== undefined && profiles.length === 0 && (
        <p className="text-sm text-fg-muted">No compliance profiles are installed.</p>
      )}

      {profiles.length > 0 && (
        <label className="flex items-center gap-2 text-sm">
          Profile
          <select
            className="rounded border border-border-strong px-2 py-1 text-sm dark:bg-slate-900"
            value={activeProfile ?? ""}
            onChange={(e) => {
              setSelected(e.target.value);
            }}
          >
            {profiles.map((p) => (
              <option key={p.id} value={p.id}>
                {p.title} ({p.id} v{p.version})
              </option>
            ))}
          </select>
        </label>
      )}

      {reportQuery.isLoading && activeProfile !== undefined && (
        <p className="text-sm text-fg-muted">Building the report…</p>
      )}

      {reportQuery.error !== null && (
        <p className="text-sm text-fg-body" role="status" data-testid="compliance-report-error">
          The report could not be built, so this profile's controls are unknown — none of them may be read as passing.
          The daemon said:{" "}
          {reportQuery.error instanceof ApiError || reportQuery.error instanceof Error
            ? reportQuery.error.message
            : "the read failed"}
        </p>
      )}

      {reportQuery.data !== undefined && (
        <>
          <p className="rounded-md border border-border-strong p-2 text-xs text-fg-muted">
            {reportQuery.data.notice}
          </p>

          <dl className="grid grid-cols-2 gap-2 sm:grid-cols-5" data-testid="compliance-summary">
            {KNOWN_CONTROL_STATUSES.map((s) => (
              <div key={s} className={`rounded-md border p-2 ${CONTROL_STATUS_CLASS[s]}`}>
                <dt className="text-xs font-medium">{CONTROL_STATUS_LABEL[s]}</dt>
                <dd className="text-lg font-semibold">{counts[s]}</dd>
              </div>
            ))}
            {counts.unknown > 0 && (
              <div className={`rounded-md border p-2 ${CONTROL_STATUS_CLASS.unknown}`}>
                <dt className="text-xs font-medium">{CONTROL_STATUS_LABEL.unknown}</dt>
                <dd className="text-lg font-semibold">{counts.unknown}</dd>
              </div>
            )}
          </dl>

          {disagrees && (
            <p className="text-xs text-amber-800 dark:text-amber-200" role="status" data-testid="compliance-summary-drift">
              The daemon's own summary does not match these counts, which happens when it reported a control status
              this build does not model. The counts above are what is actually on screen.
            </p>
          )}

          {reportQuery.data.caveats !== undefined && reportQuery.data.caveats.length > 0 && (
            <div className="rounded-md border border-amber-300 p-2 text-xs dark:border-amber-700">
              <p className="font-medium">What this report could not establish:</p>
              <ul className="mt-0.5 list-disc pl-4">
                {reportQuery.data.caveats.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            </div>
          )}

          <ul className="flex flex-col gap-2" data-testid="compliance-control-list">
            {controls.map((control) => (
              <li
                key={control.id}
                className={`rounded-md border p-3 ${CONTROL_STATUS_CLASS[control.classified]}`}
                data-testid={`compliance-control-${control.id}`}
                data-status={control.classified}
              >
                <p className="text-sm font-medium">
                  <span className="font-mono">{control.id}</span> — {control.title}
                </p>
                <p className="mt-0.5 text-xs uppercase tracking-wide">
                  {CONTROL_STATUS_LABEL[control.classified]}
                  {control.classified === "unknown" && <span className="ml-1 font-mono normal-case">({control.status})</span>}
                </p>
                <p className="mt-0.5 text-xs text-fg-muted">
                  {CONTROL_STATUS_MEANING[control.classified]}
                </p>
                {control.statement !== "" && (
                  <p className="mt-1 text-sm text-fg-muted">{control.statement}</p>
                )}
                {control.classified === "unmapped" && (
                  <p className="mt-1 text-xs" data-testid={`compliance-unmapped-${control.id}`}>
                    {control.unmappedReason !== undefined && control.unmappedReason !== ""
                      ? control.unmappedReason
                      : "The profile stated no reason for leaving this control unmapped."}
                  </p>
                )}
                {control.evidence !== undefined && control.evidence.length > 0 && (
                  <ul className="mt-1 list-disc pl-4 text-xs">
                    {control.evidence.map((e) => (
                      <li key={`${e.kind}:${e.name}`}>
                        <span className="font-mono">
                          {e.kind}:{e.name}
                        </span>{" "}
                        — {e.status}: {e.detail}
                        {e.note !== undefined && e.note !== "" && (
                          <span className="block text-fg-muted">{e.note}</span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
                {(control.evidence === undefined || control.evidence.length === 0) &&
                  control.classified !== "unmapped" && (
                    <p className="mt-1 text-xs text-fg-muted">
                      The report carried no evidence items for this control, so nothing here explains its status.
                    </p>
                  )}
              </li>
            ))}
          </ul>

          {reportQuery.data.unmappedChecks !== undefined && reportQuery.data.unmappedChecks.length > 0 && (
            <div className="rounded-md border border-border-strong p-2 text-xs">
              <p className="font-medium">
                Checks this build can emit that no control in this profile maps ({reportQuery.data.unmappedChecks.length}):
              </p>
              <p className="mt-0.5 font-mono">{reportQuery.data.unmappedChecks.join(", ")}</p>
              <p className="mt-0.5 text-fg-muted">
                Computed from: {reportQuery.data.checkUniverse}
              </p>
            </div>
          )}
        </>
      )}
    </section>
  );
}
