import { useEffect } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./layout/AppShell";
import { RequireAuth } from "./routes/RequireAuth";
import { LoginPage } from "./pages/LoginPage";
import { TopologyPage } from "./pages/TopologyPage";
import { SdnPage } from "./pages/SdnPage";
import { FirewallPage } from "./pages/FirewallPage";
import { IpamPage } from "./pages/IpamPage";
import { HistoryPage } from "./pages/HistoryPage";
import { AuditPage } from "./pages/AuditPage";
import { ToolsPage } from "./pages/ToolsPage";
import { SettingsPage } from "./pages/SettingsPage";
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
            <Route index element={<Navigate to="/topology" replace />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/sdn" element={<SdnPage />} />
            <Route path="/firewall" element={<FirewallPage />} />
            <Route path="/ipam" element={<IpamPage />} />
            <Route path="/history" element={<HistoryPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/tools" element={<ToolsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/topology" replace />} />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
