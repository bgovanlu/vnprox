// SPDX-License-Identifier: Apache-2.0

// Relays web/public/sw.js's notificationclick handler (which posts
// {type:"vnprox-push-navigate", url} to every focused/open client, per
// that file's own doc comment) into an in-app react-router navigation.
// Mounted once, app-wide, in AppShell — the same "one instance" pattern
// ChangesetDrawer/CommandPalette/etc already follow there.
//
// This component does nothing else: it does not read the deep link's url
// for anything beyond navigate(), never inspects notification content, and
// grants no capability — the page the navigation lands on (e.g.
// ChangesetReviewPage, gated behind RequireAuth like every route) is what
// actually enforces whether this session may do anything there. See
// sw.js's own doc comment on why notificationclick is a navigation only.
import { useEffect } from "react";
import { useNavigate } from "react-router-dom";

interface PushNavigateMessage {
  type: "vnprox-push-navigate";
  url: string;
}

function isPushNavigateMessage(data: unknown): data is PushNavigateMessage {
  return (
    typeof data === "object" &&
    data !== null &&
    (data as { type?: unknown }).type === "vnprox-push-navigate" &&
    typeof (data as { url?: unknown }).url === "string"
  );
}

export function PushNavigationBridge() {
  const navigate = useNavigate();

  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;

    function handleMessage(event: MessageEvent): void {
      if (isPushNavigateMessage(event.data)) {
        void navigate(event.data.url);
      }
    }

    navigator.serviceWorker.addEventListener("message", handleMessage);
    return () => {
      navigator.serviceWorker.removeEventListener("message", handleMessage);
    };
  }, [navigate]);

  return null;
}
