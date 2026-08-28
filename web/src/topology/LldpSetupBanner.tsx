// SPDX-License-Identifier: Apache-2.0

// T-3602: the "no LLDP data" notice, and the button that does something
// about it.
//
// Extracted from TopologyPage.tsx rather than left inline, for the same
// reason StalenessBanner and UnrefFindingsBanner are their own files: the
// page component is too tangled in routing, session and query state to test
// directly, and "does a read-only session see a mutating button" is exactly
// the kind of thing that must be provable rather than eyeballed.
//
// Presentational. The mutation, its pending state and its results belong to
// the page; this decides what is rendered from them.
import type { LldpInstallNodeResult } from "../api/types";
import { NodeResultsList, OperationalActionButton } from "../findings/OperationalActionButton";

export interface LldpSetupBannerProps {
  /** False when the topology already has lldp-neighbor nodes — nothing to
   * offer, so nothing renders at all. */
  show: boolean;
  /** Phase 36 Tier 2 gate. Without it the notice still renders, with its
   * documentation link: a read-only operator is not helped by a disabled
   * button, and the link is the thing they can actually act on. */
  canInstall: boolean;
  pending: boolean;
  results: readonly LldpInstallNodeResult[] | undefined;
  onInstall: () => void;
}

export function LldpSetupBanner({ show, canInstall, pending, results, onInstall }: LldpSetupBannerProps) {
  if (!show) return null;
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-status-degraded bg-status-degraded-soft px-3 py-2 text-xs text-status-degraded print:hidden">
      <span>
        No LLDP data yet — the physical layer shows NICs only.{" "}
        <a
          href="https://man7.org/linux/man-pages/man8/lldpd.8.html"
          className="underline"
          target="_blank"
          rel="noreferrer"
        >
          Set up lldpd
        </a>{" "}
        to see real switch names and ports.
      </span>
      {/* The backend for this has existed since T-605 (POST /lldp/install,
          netWrite + CSRF + explicit confirm, fanning out to every peer and
          auditing each node's outcome as `lldp.install`). The banner only
          ever linked to a man page. */}
      {canInstall && (
        <OperationalActionButton
          label="Install lldpd on all nodes"
          title="Install lldpd on every node?"
          description="vnprox will install and enable the lldpd package on every reachable node in this cluster. This changes no network configuration — no changeset is staged — but it does install software and start a service, and each node's outcome is written to the audit log."
          confirmLabel="Install"
          pending={pending}
          result={results && <NodeResultsList results={results} />}
          onConfirm={onInstall}
        />
      )}
    </div>
  );
}
