// SPDX-License-Identifier: Apache-2.0

// T-3002: the governance screen.
//
// Fifteen routes across four features that a `curl` could reach and an
// operator could not: policy-as-code (`GET`/`PUT /policies`,
// `POST /policies/test`), compliance profiles (`GET /compliance`,
// `GET /compliance/{profile}`), tenant administration (eight routes), and the
// digest schedule (`GET`/`PUT /digest/schedule`). T-4006 added a fifth,
// `GET /calendar`: freeze windows (declared as policy rules on the Policies
// tab) alongside pending scheduled changesets, on one screen.
//
// The break-glass override of the two-person rule is deliberately NOT here:
// it belongs inside the review screen where it blocks, not on an
// administration page an operator would have to think to visit. See
// changesets/PolicyVerdictPanel.tsx and changesets/BreakGlassPanel.tsx. The
// freeze-window override (T-4006) follows the same split — see
// changesets/FreezeOverridePanel.tsx — while the calendar's READ half lives
// here beside the policy rules it renders, the same way PoliciesPanel does.
//
// Nothing on this page applies anything. `PUT /policies` replaces a document,
// the tenant routes write tenant rows, and the digest route writes a
// schedule — none of them is a network mutation, and none of them goes near
// the change engine.
import { PageHeader } from "../components/PageHeader";
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from "../components/Tabs";
import { HelpAnchor } from "../help/HelpAnchor";
import { PoliciesPanel } from "./PoliciesPanel";
import { CalendarPanel } from "./CalendarPanel";
import { CompliancePanel } from "./CompliancePanel";
import { TenantsPanel } from "./TenantsPanel";
import { DigestSchedulePanel } from "./DigestSchedulePanel";

export function GovernancePage() {
  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto">
      <TabsRoot defaultValue="policies" className="flex flex-1 flex-col">
        <PageHeader
          title={
            <>
              Governance
              <HelpAnchor topic="governance-page" />
            </>
          }
          description="The rules a change is measured against, what the cluster can evidence about itself, who may see what, and
            when the summary goes out. Every refusal these produce happens in the daemon; this screen reads and
            administers them, and enforces nothing of its own."
          tabs={
            <TabsList>
              <TabsTrigger value="policies">Policies</TabsTrigger>
              <TabsTrigger value="calendar">Calendar</TabsTrigger>
              <TabsTrigger value="compliance">Compliance</TabsTrigger>
              <TabsTrigger value="tenants">Tenants</TabsTrigger>
              <TabsTrigger value="digest">Digest</TabsTrigger>
            </TabsList>
          }
        />

        <TabsContent value="policies" className="mt-4 flex-1">
          <PoliciesPanel />
        </TabsContent>
        <TabsContent value="calendar" className="mt-4 flex-1">
          <CalendarPanel />
        </TabsContent>
        <TabsContent value="compliance" className="mt-4 flex-1">
          <CompliancePanel />
        </TabsContent>
        <TabsContent value="tenants" className="mt-4 flex-1">
          <TenantsPanel />
        </TabsContent>
        <TabsContent value="digest" className="mt-4 flex-1">
          <DigestSchedulePanel />
        </TabsContent>
      </TabsRoot>
    </div>
  );
}
