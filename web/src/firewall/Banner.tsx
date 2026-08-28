// SPDX-License-Identifier: Apache-2.0

// The "Datacenter firewall is OFF: none of these rules are active" banner
// (docs/features/firewall.md §2 — "the classic PVE footgun made visible")
// and its cascaded variants at node/guest scope. Pure/presentational: all
// banner-cascade logic lives server-side in internal/fw.ScopeBanners, this
// component only renders whatever BannerView[] it's given.
import type { BannerView } from "../api/types";

export function FirewallBanners({ banners }: { banners: BannerView[] | undefined }) {
  if (!banners || banners.length === 0) {
    return null;
  }
  return (
    <div className="flex flex-col gap-1.5" role="alert">
      {banners.map((b, i) => (
        <div
          key={`${b.scope}-${String(i)}`}
          className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
        >
          {b.message}
        </div>
      ))}
    </div>
  );
}
