import { useEffect } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./layout/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { DesktopOnlyRoute } from "./routes/DesktopOnlyRoute";
import { LoginPage } from "./pages/LoginPage";
import { DashboardPage } from "./pages/DashboardPage";
import { GlobalTopologyGate } from "./topology/federation/GlobalTopologyGate";
import { GuestsPage } from "./pages/GuestsPage";
import { SdnPage } from "./pages/SdnPage";
import { FirewallPage } from "./pages/FirewallPage";
import { FlowExplorerPage } from "./pages/FlowExplorerPage";
import { ConntrackPage } from "./pages/ConntrackPage";
import { EdgePage } from "./pages/EdgePage";
import { DiagnosePage } from "./pages/DiagnosePage";
import { IpamPage } from "./pages/IpamPage";
import { ManagementPage } from "./pages/ManagementPage";
import { PortsPage } from "./pages/PortsPage";
import { BlueprintsPage } from "./pages/BlueprintsPage";
import { HubPage } from "./hub/HubPage";
import { HistoryPage } from "./pages/HistoryPage";
import { IncidentsPage } from "./pages/IncidentsPage";
import { AuditPage } from "./pages/AuditPage";
import { ToolsPage } from "./pages/ToolsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { AlertRulesPage } from "./pages/AlertRulesPage";
import { FederationClustersPage } from "./pages/FederationClustersPage";
import { CertificatesPage } from "./pages/CertificatesPage";
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
              path="/diagnose"
              element={
                <DesktopOnlyRoute pageLabel="Diagnose">
                  <DiagnosePage />
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
