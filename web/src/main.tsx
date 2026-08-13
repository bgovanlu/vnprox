import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { queryClient } from "./lib/queryClient";
import { ToastProvider } from "./components/Toast";
import { App } from "./App";
import { registerServiceWorker } from "./push/registerServiceWorker";
import "./index.css";

// T-2005: PWA installability + offline shell + web push all require the
// service worker to be registered. Called once, at startup, before the
// first render — the same "do this unconditionally at the composition
// root" placement queryClient's own construction already uses in this file.
registerServiceWorker();

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("#root element not found");
}

createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <App />
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
