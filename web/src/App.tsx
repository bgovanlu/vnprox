// SPDX-License-Identifier: Apache-2.0

import { useEffect } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./layout/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { DesktopOnlyRoute } from "./routes/DesktopOnlyRoute";
import { LoginPage } from "./pages/LoginPage";
import { DashboardPage } from "./pages/DashboardPage";
import { GlobalTopologyGate } from "./topology/federation/GlobalTopologyGate";
import { GuestsPage } from "./pages/GuestsPage";
import { GuestEgoPage } from "./pages/GuestEgoPage";
import { SdnPage } from "./pages/SdnPage";
import { FirewallPage } from "./pages/FirewallPage";
import { FlowExplorerPage } from "./pages/FlowExplorerPage";
import { ConntrackPage } from "./pages/ConntrackPage";
import { EdgePage } from "./pages/EdgePage";
import { CompiledRulesetPage } from "./pages/CompiledRulesetPage";
import { RouteExplorerPage } from "./pages/RouteExplorerPage";
import { DiagnosePage } from "./pages/DiagnosePage";
import { AnalysisPage } from "./pages/AnalysisPage";
import { IpamPage } from "./pages/IpamPage";
import { ManagementPage } from "./pages/ManagementPage";
import { PortsPage } from "./pages/PortsPage";
import { CablingPlanPage } from "./pages/CablingPlanPage";
import { BlueprintsPage } from "./pages/BlueprintsPage";
import { HubPage } from "./hub/HubPage";
import { ConfigAsCodePage } from "./drift/ConfigAsCodePage";
import { GovernancePage } from "./governance/GovernancePage";
import { HistoryPage } from "./pages/HistoryPage";
import { IncidentsPage } from "./pages/IncidentsPage";
import { AuditPage } from "./pages/AuditPage";
import { ToolsPage } from "./pages/ToolsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { AlertRulesPage } from "./pages/AlertRulesPage";
import { FederationClustersPage } from "./pages/FederationClustersPage";
import { CertificatesPage } from "./pages/CertificatesPage";
import { PlatformPanel } from "./settings/PlatformPanel";
import { ChangesetReviewPage } from "./changesets/ChangesetReviewPage";
import { EmbedFrame } from "./embed/EmbedFrame";
import { EmbedMap } from "./embed/EmbedMap";
import { EmbedDashboard } from "./embed/EmbedDashboard";
import { EmbedPosture } from "./embed/EmbedPosture";
import { applyThemeClass, useThemeStore } from "./store/theme";
import { detectDemoMode } from "./demo/useDemoMode";
import { detectPublicDemo } from "./tour/usePublicDemo";

export function App() {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    applyThemeClass(theme);
  }, [theme]);

  // T-2801: ask the daemon once whether it is a demo, and stamp
  // <html class="demo"> if so. Empty dependency list on purpose — a
  // daemon does not stop being a demo while you look at it.
  useEffect(() => {
    void detectDemoMode();
  }, []);

  // T-2802: and once whether it is the HOSTED demo, which is a different
  // question — a public instance has an edge in front of it that refuses
  // every write and hands each visitor their own session. Asked of the edge
  // itself (a route no normal daemon serves), so a daemon without one
  // cannot answer it wrongly.
  useEffect(() => {
    void detectPublicDemo();
  }, []);

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        {/* T-1706: read-only, token-scoped embeds for wikis / NOC screens /
         * status pages. Deliberately outside RequireAuth and AppShell —
         * an embed authenticates with its `?token=` embed token (via
         * EmbedFrame → apiFetch), never a session cookie, and carries no
         * navigation or mutation chrome. */}
        <Route
          path="/embed/map"
          element={
            <EmbedFrame title="Network map">
              <EmbedMap />
            </EmbedFrame>
          }
        />
        <Route
          path="/embed/dashboard"
          element={
            <EmbedFrame title="Dashboard">
              <EmbedDashboard />
            </EmbedFrame>
          }
        />
        <Route
          path="/embed/posture"
          element={
            <EmbedFrame title="Network posture">
              <EmbedPosture />
            </EmbedFrame>
          }
        />
        <Route element={<RequireAuth />}>
          <Route element={<AppShell />}>
            {/* T-909: the narrow-viewport reachable set is Dashboard,
             * Findings (/tools, itself internally restricted to a read-only
             * findings view at narrow width — see ToolsPage.tsx), and the
             * changeset confirm/rollback overlay (mounted app-wide in
             * AppShell, not routed). Every other route is wrapped in
             * DesktopOnlyRoute so navigating to it at narrow width —
             * including a direct/bookmarked link — renders an explicit
             * "desktop only" affordance instead of a broken/cramped
             * attempt at the full page. */}
            <Route index element={<DashboardPage />} />
            <Route
              path="/topology"
              element={
                <DesktopOnlyRoute pageLabel="Topology">
                  <GlobalTopologyGate />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/management"
              element={
                <DesktopOnlyRoute pageLabel="Management">
                  <ManagementPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/guests"
              element={
                <DesktopOnlyRoute pageLabel="Guests">
                  <GuestsPage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3906: the guest ego view. Not wrapped by a picker-only
              * assumption — with no `?ref=` it renders its own picker, so a
              * direct nav-rail/bookmark hit always lands somewhere useful. */}
            <Route
              path="/guest"
              element={
                <DesktopOnlyRoute pageLabel="Guest view">
                  <GuestEgoPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/sdn"
              element={
                <DesktopOnlyRoute
                  pageLabel="SDN"
                  detail="SDN zone/vnet/subnet editing and wizards need a desktop-sized screen. Open vnprox on a desktop or a wider window to make changes here."
                >
                  <SdnPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/firewall"
              element={
                <DesktopOnlyRoute pageLabel="Firewall">
                  <FirewallPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/ipam"
              element={
                <DesktopOnlyRoute pageLabel="IPAM">
                  <IpamPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/flows"
              element={
                <DesktopOnlyRoute pageLabel="Flows">
                  <FlowExplorerPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/conntrack"
              element={
                <DesktopOnlyRoute pageLabel="Conntrack">
                  <ConntrackPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/edge"
              element={
                <DesktopOnlyRoute pageLabel="Edge">
                  <EdgePage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/route-explorer"
              element={
                <DesktopOnlyRoute pageLabel="Route explorer">
                  <RouteExplorerPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/firewall/compiled"
              element={
                <DesktopOnlyRoute pageLabel="Compiled ruleset">
                  <CompiledRulesetPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/diagnose"
              element={
                <DesktopOnlyRoute pageLabel="Diagnose">
                  <DiagnosePage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3004: the analysis surfaces — failure simulation, capacity
             * export, QoS shaping, PBS backup paths, IPv6 segments. Desktop
             * only like every other dense read screen. */}
            <Route
              path="/analysis"
              element={
                <DesktopOnlyRoute pageLabel="Analysis">
                  <AnalysisPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/ports"
              element={
                <DesktopOnlyRoute pageLabel="Ports">
                  <PortsPage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3907: the physical cabling plan — printable, LLDP-derived. */}
            <Route
              path="/cabling"
              element={
                <DesktopOnlyRoute pageLabel="Cabling plan">
                  <CablingPlanPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/blueprints"
              element={
                <DesktopOnlyRoute pageLabel="Blueprints">
                  <BlueprintsPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/hub"
              element={
                <DesktopOnlyRoute pageLabel="Hub">
                  <HubPage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3001: the config-as-code cockpit — the git sync's status,
              * the spec document and its plan, and the two reconciliation
              * actions. Desktop-only like every other editing surface: it
              * carries a YAML editor and two confirmations. */}
            <Route
              path="/config-as-code"
              element={
                <DesktopOnlyRoute pageLabel="Config as code">
                  <ConfigAsCodePage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3002: the governance surfaces — policy-as-code, compliance
              * profiles, tenant administration and the digest schedule.
              * Desktop-only like every other dense administration screen; the
              * policy DENY verdict and the break-glass override deliberately
              * live in the review screen instead, where they block. */}
            <Route
              path="/governance"
              element={
                <DesktopOnlyRoute pageLabel="Governance">
                  <GovernancePage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/history"
              element={
                <DesktopOnlyRoute pageLabel="History">
                  <HistoryPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/incidents"
              element={
                <DesktopOnlyRoute pageLabel="Incidents">
                  <IncidentsPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/audit"
              element={
                <DesktopOnlyRoute pageLabel="Audit">
                  <AuditPage />
                </DesktopOnlyRoute>
              }
            />
            <Route path="/tools" element={<ToolsPage />} />
            {/* T-2003: the shareable review link. Deliberately NOT wrapped in
             * DesktopOnlyRoute — the exit demo requires this to work "from a
             * phone" (planning/tasks/phase-20.md), matching the narrow-
             * viewport-reachable precedent T-909 already set for the
             * changeset confirm/rollback overlay. */}
            <Route path="/changesets/:id/review" element={<ChangesetReviewPage />} />
            <Route
              path="/settings"
              element={
                <DesktopOnlyRoute pageLabel="Settings">
                  <SettingsPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/settings/alert-rules"
              element={
                <DesktopOnlyRoute pageLabel="Alert rules">
                  <AlertRulesPage />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/settings/certificates"
              element={
                <DesktopOnlyRoute pageLabel="Certificates">
                  <CertificatesPage />
                </DesktopOnlyRoute>
              }
            />
            {/* T-3003: tokens, webhooks, plugin lifecycle and the daemon's
              * live self-check. A Settings sub-route like alert-rules and
              * certificates, so it inherits the sidebar's Settings entry
              * rather than claiming a top-level glyph of its own. */}
            <Route
              path="/settings/platform"
              element={
                <DesktopOnlyRoute pageLabel="Platform">
                  <PlatformPanel />
                </DesktopOnlyRoute>
              }
            />
            <Route
              path="/settings/federation"
              element={
                <DesktopOnlyRoute pageLabel="Federated clusters">
                  <FederationClustersPage />
                </DesktopOnlyRoute>
              }
            />
            <Route path="*" element={<Navigate to="/topology" replace />} />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
