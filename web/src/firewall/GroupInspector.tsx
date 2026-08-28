// SPDX-License-Identifier: Apache-2.0

// Security-group inspector (T-2002, closing the gap T-1603's report
// flagged): "groups are where microsegmentation policy actually lives",
// but until now the only way to launch the microseg planner was per-guest
// (FirewallPage's GuestPanel). This surface shows a group's own rule list
// (GET /firewall/rulesets?scope=group, added alongside this component) and
// every guest its rules actually reach (GET /firewall/effects, T-502 AC4's
// rule-effects preview, reused verbatim — not recomputed here), then lets
// a reviewer launch the SAME <MicrosegPlanner guestRef=.../> component the
// guest-scoped launch uses, for one of those guests. Deliberately not a
// second synthesis path: the planner is guest-ref-driven at the API level
// (POST /microseg/propose takes a guest ref only, T-1602), so reusing the
// identical component is what guarantees the group-scoped launch produces
// the exact same proposal shape as the guest-scoped one (T-2002 AC3) — not
// a contract this component has to keep in sync by hand.
import { useState } from "react";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { HelpAnchor } from "../help/HelpAnchor";
import { MicrosegPlanner } from "../microseg/MicrosegPlanner";
import { ruleMatchLabel } from "./format";
import { useFirewallEffectsQuery, useGroupRulesetQuery } from "./queries";

export interface GroupInspectorProps {
  name: string;
  onBack: () => void;
}

export function GroupInspector({ name, onBack }: GroupInspectorProps) {
  const { data, isLoading, error } = useGroupRulesetQuery(name);
  const { data: effects, isLoading: effectsLoading } = useFirewallEffectsQuery(name);
  const [selectedGuest, setSelectedGuest] = useState<string | undefined>(undefined);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">
          Security group: <span className="font-mono">{name}</span>
        </h2>
        <button
          type="button"
          onClick={onBack}
          className="rounded-md px-2 py-1 text-sm text-accent-700 hover:underline dark:text-accent-400"
        >
          ← Back to objects
        </button>
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading group…</p>}
      {error && (
        <EmptyState
          title="Could not load this group"
          description="It may have been deleted or renamed since you opened this page."
        />
      )}

      {data && (
        <>
          {data.comment && <p className="text-sm text-slate-600 dark:text-slate-400">{data.comment}</p>}

          <div>
            <h3 className="mb-1 text-sm font-semibold">This group&apos;s own rules</h3>
            {data.rules.length === 0 ? (
              <EmptyState title="No rules" description="This security group has no rules of its own yet." />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Pos</TableHead>
                    <TableHead>Enabled</TableHead>
                    <TableHead>Direction</TableHead>
                    <TableHead>Action</TableHead>
                    <TableHead>Match</TableHead>
                    <TableHead>Comment</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.rules.map((r) => (
                    <TableRow key={r.pos} className={r.enabled ? undefined : "opacity-50"}>
                      <TableCell>{r.pos}</TableCell>
                      <TableCell>{r.enabled ? "Yes" : "No"}</TableCell>
                      <TableCell className="uppercase">{r.direction}</TableCell>
                      <TableCell className="font-mono">{r.action}</TableCell>
                      <TableCell>{ruleMatchLabel(r)}</TableCell>
                      <TableCell>{r.comment ?? "—"}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <h3 className="flex items-center gap-1.5 text-sm font-semibold">
              Guests this group reaches
              <HelpAnchor topic="firewall-rule-effects" />
            </h3>
            {effectsLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Computing matched guests…</p>}
            {effects && effects.guests.length === 0 && (
              <p className="text-sm text-amber-600 dark:text-amber-400">
                This group matches no guests yet — it isn&apos;t referenced by any rule anywhere reachable.
              </p>
            )}
            {effects && effects.guests.length > 0 && (
              <div className="flex flex-col gap-3">
                <select
                  aria-label="Select a guest to plan for"
                  className="w-fit rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-800"
                  value={selectedGuest ?? ""}
                  onChange={(e) => { setSelectedGuest(e.target.value === "" ? undefined : e.target.value); }}
                >
                  <option value="">Choose a guest to launch the planner for…</option>
                  {effects.guests.map((g) => (
                    <option key={g} value={g}>
                      {g}
                    </option>
                  ))}
                </select>
                {selectedGuest && (
                  <div className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
                    <MicrosegPlanner guestRef={selectedGuest} />
                  </div>
                )}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
