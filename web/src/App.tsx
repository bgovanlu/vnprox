import { useEffect } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./layout/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { DesktopOnlyRoute } from "./routes/DesktopOnlyRoute";
import { LoginPage } from "./pages/LoginPage";
import { DashboardPage } from "./pages/DashboardPage";
import { TopologyPage } from "./pages/TopologyPage";
import { GuestsPage } from "./pages/GuestsPage";
import { SdnPage } from "./pages/SdnPage";
import { FirewallPage } from "./pages/FirewallPage";
import { IpamPage } from "./pages/IpamPage";
import { ManagementPage } from "./pages/ManagementPage";
import { PortsPage } from "./pages/PortsPage";
import { BlueprintsPage } from "./pages/BlueprintsPage";
import { HistoryPage } from "./pages/HistoryPage";
import { AuditPage } from "./pages/AuditPage";
import { ToolsPage } from "./pages/ToolsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { AlertRulesPage } from "./pages/AlertRulesPage";
import { applyThemeClass, useThemeStore } from "./store/theme";

export function App() {
  const theme = useThemeStore((s) => s.theme);

  useEffect(() => {
    applyThemeClass(theme);
  }, [theme]);

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
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
                  <TopologyPage />
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
              path="/history"
              element={
                <DesktopOnlyRoute pageLabel="History">
                  <HistoryPage />
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
            <Route path="*" element={<Navigate to="/topology" replace />} />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
