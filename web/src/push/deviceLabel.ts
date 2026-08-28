// SPDX-License-Identifier: Apache-2.0

// A short, best-effort "what device is this" label offered as the default
// for POST /push/subscriptions' deviceLabel field (display-only, capped at
// 120 runes server-side — internal/api/push.go's maxDeviceLabelRunes). Not
// meant to be exact: it exists so "Your devices" (PushSettingsSection.tsx)
// doesn't show an unlabeled list an operator has to disambiguate by
// creation time alone. The user can always override it before subscribing.
export function guessDeviceLabel(userAgent: string): string {
  const platform = guessPlatform(userAgent);
  const browser = guessBrowser(userAgent);
  if (platform && browser) return `${platform} — ${browser}`;
  return platform || browser || "This device";
}

function guessPlatform(ua: string): string {
  if (ua.includes("iPhone")) return "iPhone";
  if (ua.includes("iPad")) return "iPad";
  if (ua.includes("Android")) return "Android";
  if (ua.includes("Macintosh")) return "Mac";
  if (ua.includes("Windows")) return "Windows";
  if (ua.includes("Linux")) return "Linux";
  return "";
}

function guessBrowser(ua: string): string {
  // Order matters: several browsers include "Safari" or "Chrome" in their
  // own UA string for compatibility, so the more specific token must be
  // checked first.
  if (ua.includes("Edg/")) return "Edge";
  if (ua.includes("OPR/")) return "Opera";
  if (ua.includes("Firefox/")) return "Firefox";
  if (ua.includes("CriOS/")) return "Chrome";
  if (ua.includes("Chrome/")) return "Chrome";
  if (ua.includes("Safari/")) return "Safari";
  return "";
}
