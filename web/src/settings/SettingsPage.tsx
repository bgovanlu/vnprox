// SPDX-License-Identifier: Apache-2.0

// The Settings page (replaces the T-005 placeholder). vnprox.toml is a
// per-node, restart-time config file, and there is no runtime preferences
// API, so this page is deliberately: editable *client* preferences
// (theme), a read-only view of how this instance is configured (GET
// /config — non-secret operational values), the current account + its
// capabilities, and shortcuts into the existing cluster-safety surfaces
// (protected interfaces, the Management page). Making config-file values
// runtime-editable would be a separate, larger backend change.
import { Link, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Button } from "../components/Button";
import { PageHeader } from "../components/PageHeader";
import { useToast } from "../components/Toast";
import { logout } from "../api/auth";
import { useSession, SESSION_QUERY_KEY } from "../api/useSession";
import type { Capabilities } from "../api/types";
import { useThemeStore, type Theme } from "../store/theme";
import { resumeOnboarding } from "../onboarding/onboardingMachine";
import { useOnboardingProgressQuery, useSaveOnboardingProgressMutation } from "../onboarding/queries";
import { useMgmtStatusQuery } from "../topology/queries";
import { useInstanceConfigQuery } from "./queries";
import { PushSettingsSection } from "../push/PushSettingsSection";

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-slate-200 p-4 dark:border-slate-700">
      <h2 className="text-sm font-semibold text-slate-800 dark:text-slate-100">{title}</h2>
      {description && <p className="mt-0.5 text-xs text-slate-600 dark:text-slate-400">{description}</p>}
      <div className="mt-3">{children}</div>
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-slate-100 py-1.5 text-sm last:border-b-0 dark:border-slate-800">
      <span className="w-52 shrink-0 text-slate-600 dark:text-slate-400">{label}</span>
      <span className="min-w-0 break-words font-medium text-slate-700 dark:text-slate-200">{children}</span>
    </div>
  );
}

function YesNo({ value, yesTone }: { value: boolean; yesTone?: "warn" }) {
  if (value) {
    const cls = yesTone === "warn" ? "text-amber-600 dark:text-amber-400" : "text-slate-700 dark:text-slate-200";
    return <span className={cls}>Yes</span>;
  }
  return <span className="text-slate-600 dark:text-slate-400">No</span>;
}

const CAP_LABELS: { key: keyof Capabilities; label: string }[] = [
  { key: "netRead", label: "View network" },
  { key: "netWrite", label: "Change network" },
  { key: "sdnRead", label: "View SDN" },
  { key: "sdnWrite", label: "Change SDN" },
  { key: "fwRead", label: "View firewall" },
  { key: "fwWrite", label: "Change firewall" },
  { key: "guestNet", label: "Change guest NICs" },
  { key: "audit", label: "View audit log" },
  { key: "capture", label: "Capture packets" },
];

/** Aggregates a capability across every node the session is scoped to: true
 * if the user holds it on any node (a plain-English "what you can do
 * somewhere in this cluster" summary). */
function hasCapAnywhere(caps: Record<string, Capabilities>, key: keyof Capabilities): boolean {
  return Object.values(caps).some((c) => c[key]);
}

export function SettingsPage() {
  const theme = useThemeStore((s) => s.theme);
  const setTheme = useThemeStore((s) => s.setTheme);
  const { data: session } = useSession();
  const { data: config } = useInstanceConfigQuery();
  const { data: mgmtStatus } = useMgmtStatusQuery();
  const { data: onboardingProgress } = useOnboardingProgressQuery();
  const saveOnboarding = useSaveOnboardingProgressMutation();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { toast } = useToast();

  async function handleLogout(): Promise<void> {
    try {
      await logout();
    } catch {
      // Best-effort — still drop client session state and redirect.
    }
    await queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY });
    void navigate("/login", { replace: true });
  }

  const caps = session?.caps ?? {};

  const protectedStatus = !mgmtStatus
    ? "…"
    : mgmtStatus.staleProtected
      ? "Out of date — a management interface moved since it was last confirmed"
      : mgmtStatus.source === "confirmed"
        ? "Confirmed during onboarding"
        : "Detected automatically — not yet confirmed";

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <PageHeader
        title="Settings"
        description={
          <>
            Your preferences, and how this vnprox instance is configured. Instance values come from{" "}
            <code className="rounded bg-slate-100 px-1 dark:bg-slate-800">/etc/vnprox/vnprox.toml</code> on this node
            and are read-only here — edit that file and restart the service to change them.
          </>
        }
      />

      <Section title="Appearance" description="Stored in this browser only.">
        <Row label="Theme">
          <div className="inline-flex overflow-hidden rounded-md border border-slate-300 dark:border-slate-600" role="radiogroup" aria-label="Theme">
            {(["light", "dark"] as Theme[]).map((t) => (
              <button
                key={t}
                type="button"
                role="radio"
                aria-checked={theme === t}
                onClick={() => {
                  setTheme(t);
                }}
                className={
                  "px-3 py-1 text-sm capitalize " +
                  (theme === t
                    ? "bg-accent-600 text-white"
                    : "bg-white text-slate-600 hover:bg-slate-100 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800")
                }
              >
                {t}
              </button>
            ))}
          </div>
        </Row>
      </Section>

      <Section title="Account">
        {session ? (
          <>
            <Row label="Signed in as">
              {session.user.username}@{session.user.realm}
            </Row>
            <Row label="You can">
              <div className="flex flex-wrap gap-1.5">
                {CAP_LABELS.filter((c) => hasCapAnywhere(caps, c.key)).map((c) => (
                  <span key={c.key} className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                    {c.label}
                  </span>
                ))}
                {CAP_LABELS.every((c) => !hasCapAnywhere(caps, c.key)) && (
                  <span className="text-xs text-slate-600 dark:text-slate-400">No capabilities granted.</span>
                )}
              </div>
            </Row>
            <div className="mt-3">
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  void handleLogout();
                }}
              >
                Sign out
              </Button>
            </div>
          </>
        ) : (
          <p className="text-sm text-slate-600 dark:text-slate-400">Not signed in.</p>
        )}
      </Section>

      <Section title="Instance" description="Read-only — from vnprox.toml on this node.">
        {config ? (
          <>
            <Row label="Version">{config.version}</Row>
            <Row label="Read-only mode">
              <YesNo value={config.readOnly} yesTone="warn" />
            </Row>
            <Row label="Dangerous-ops override">
              <YesNo value={config.allowDangerousOps} yesTone="warn" />
            </Row>
            <Row label="Default confirm window">{config.confirmTimeoutDefaultSec}s</Row>
            <Row label="Snapshot retention">
              keep {config.snapshotKeepDays} days · pin {config.snapshotPinDays} days
            </Row>
            <Row label="Poll intervals">
              PVE {config.pveInterval} · host {config.hostInterval} · LLDP {config.lldpInterval}
            </Row>
            <Row label="PVE API">{config.pveApiUrl}</Row>
            <Row label="Listen address">{config.listen}</Row>
            <Row label="Protected-set path">
              <code className="text-xs">{config.protectedPath}</code>
            </Row>
          </>
        ) : (
          <p className="text-sm text-slate-600 dark:text-slate-400">Loading instance configuration…</p>
        )}
      </Section>

      <Section title="Cluster & safety">
        <Row label="Protected interfaces">{protectedStatus}</Row>
        <div className="mt-3 flex flex-wrap gap-2">
          {onboardingProgress && (
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                saveOnboarding.mutate({ ...resumeOnboarding(onboardingProgress), currentStep: "protected" });
                toast({ title: "Opening protected-interfaces review" });
              }}
            >
              Review protected interfaces
            </Button>
          )}
          <Link to="/management">
            <Button size="sm" variant="secondary">
              Manage interfaces
            </Button>
          </Link>
        </div>
      </Section>

      <Section title="Alerting" description="Route findings/drift transitions to a webhook (Gotify, ntfy, Slack, or generic JSON).">
        <Link to="/settings/alert-rules">
          <Button size="sm" variant="secondary">
            Manage alert rules
          </Button>
        </Link>
      </Section>

      <Section
        title="Notifications"
        description="Web push to this device (and any other you've enabled it on) for critical findings, changesets awaiting confirm, and drift — installable as an app, per docs/roadmap-universal.md's on-call phone workflow."
      >
        <PushSettingsSection />
      </Section>

      <Section
        title="Certificates"
        description="Expiry, name coverage, and chain to the cluster CA for every node's TLS certificate — what cross-node traffic depends on."
      >
        <Link to="/settings/certificates">
          <Button size="sm" variant="secondary">
            View certificates
          </Button>
        </Link>
      </Section>

      <Section title="Federation" description="Attach other Proxmox clusters for aggregated views and cross-cluster IPAM conflict detection.">
        <Link to="/settings/federation">
          <Button size="sm" variant="secondary">
            Manage federated clusters
          </Button>
        </Link>
      </Section>

      <Section
        title="Platform"
        description="Automation tokens and their effective scope, webhook delivery targets, installed plugins and what each may touch, and the daemon's own live self-check."
      >
        <Link to="/settings/platform">
          <Button size="sm" variant="secondary">
            Open platform panel
          </Button>
        </Link>
      </Section>

      <Section title="About">
        <Row label="vnprox">{config?.version ? `v${config.version}` : "visual networking add-on for Proxmox VE"}</Row>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-sm">
          <a className="text-accent-700 hover:underline dark:text-accent-400" href="https://github.com/bgovanlu/vnprox" target="_blank" rel="noreferrer">
            GitHub
          </a>
          <a className="text-accent-700 hover:underline dark:text-accent-400" href="https://github.com/bgovanlu/vnprox/issues" target="_blank" rel="noreferrer">
            Report an issue
          </a>
        </div>
      </Section>
    </div>
  );
}
