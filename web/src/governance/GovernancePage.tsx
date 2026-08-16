// T-3002: the governance screen.
//
// Fifteen routes across four features that a `curl` could reach and an
// operator could not: policy-as-code (`GET`/`PUT /policies`,
// `POST /policies/test`), compliance profiles (`GET /compliance`,
// `GET /compliance/{profile}`), tenant administration (eight routes), and the
// digest schedule (`GET`/`PUT /digest/schedule`).
//
// The fifth surface this card owns is deliberately NOT here: a `deny`
// verdict, and the break-glass override of the two-person rule, belong inside
// the review screen where they block, not on an administration page an
// operator would have to think to visit. See
// changesets/PolicyVerdictPanel.tsx and changesets/BreakGlassPanel.tsx.
//
// Nothing on this page applies anything. `PUT /policies` replaces a document,
// the tenant routes write tenant rows, and the digest route writes a
// schedule — none of them is a network mutation, and none of them goes near
// the change engine.
import * as RadixTabs from "@radix-ui/react-tabs";
import { HelpAnchor } from "../help/HelpAnchor";
import { PoliciesPanel } from "./PoliciesPanel";
import { CompliancePanel } from "./CompliancePanel";
import { TenantsPanel } from "./TenantsPanel";
import { DigestSchedulePanel } from "./DigestSchedulePanel";

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-sm font-medium text-slate-500 data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:text-slate-400 dark:data-[state=active]:text-accent-400";

export function GovernancePage() {
  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto">
      <div>
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          Governance
          <HelpAnchor topic="governance-page" />
        </h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          The rules a change is measured against, what the cluster can evidence about itself, who may see what, and
          when the summary goes out. Every refusal these produce happens in the daemon; this screen reads and
          administers them, and enforces nothing of its own.
        </p>
      </div>

      <RadixTabs.Root defaultValue="policies" className="flex flex-1 flex-col">
        <RadixTabs.List className="flex gap-1 border-b border-slate-200 dark:border-slate-700">
          <RadixTabs.Trigger value="policies" className={tabTriggerClass}>
            Policies
          </RadixTabs.Trigger>
          <RadixTabs.Trigger value="compliance" className={tabTriggerClass}>
            Compliance
          </RadixTabs.Trigger>
          <RadixTabs.Trigger value="tenants" className={tabTriggerClass}>
            Tenants
          </RadixTabs.Trigger>
          <RadixTabs.Trigger value="digest" className={tabTriggerClass}>
            Digest
          </RadixTabs.Trigger>
        </RadixTabs.List>

        <RadixTabs.Content value="policies" className="mt-4 flex-1">
          <PoliciesPanel />
        </RadixTabs.Content>
        <RadixTabs.Content value="compliance" className="mt-4 flex-1">
          <CompliancePanel />
        </RadixTabs.Content>
        <RadixTabs.Content value="tenants" className="mt-4 flex-1">
          <TenantsPanel />
        </RadixTabs.Content>
        <RadixTabs.Content value="digest" className="mt-4 flex-1">
          <DigestSchedulePanel />
        </RadixTabs.Content>
      </RadixTabs.Root>
    </div>
  );
}
