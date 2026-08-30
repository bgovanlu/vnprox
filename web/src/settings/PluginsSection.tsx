// SPDX-License-Identifier: Apache-2.0

// Installed plugins and their lifecycle (T-1702's four routes).
//
// THIS IS NOT AN INSTALL SURFACE, and adding one here would be a second
// install path around the trust gate. `/api/v1/plugins` has no install
// operation: installing goes through `POST /hub/install` (already wired in
// `web/src/hub/`), which verifies the artifact's Ed25519 signature against
// the same trust store blueprint bundles use, refuses a manifest whose
// declared capability scope disagrees with the catalog listing the operator
// consented to (`capabilityMismatch` — the one status no trust flag can
// override, T-2104/T-2904), and only then registers through the registry,
// which re-validates the scope independently. What was headless is the
// *lifecycle* of an already-installed plugin, and that is all this section
// does.
//
// The capability list is rendered as the ceiling it is, not as a description:
// it bounds which seams the plugin may touch and which change-engine op
// classes it may stage, and no plugin can ever exceed it — including one
// whose registry listing claimed something else.
import { useState } from "react";
import clsx from "clsx";
import { Link } from "react-router-dom";
import { Button } from "../components/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useToast } from "../components/Toast";
import { ApiError } from "../api/client";
import type { Plugin } from "../api/types";
import { usePluginLifecycleMutation, usePluginsQuery, type PluginLifecycleAction } from "./platformQueries";
import { PlatformSection, RefusalNotice, ScopeChips, UnixTime } from "./platformCommon";

function EnabledBadge({ plugin }: { plugin: Plugin }) {
  return (
    <span
      data-testid={`plugin-state-${plugin.id}`}
      data-enabled={plugin.enabled ? "true" : "false"}
      className={clsx(
        "rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide",
        plugin.enabled
          ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
          : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400",
      )}
    >
      {plugin.enabled ? "enabled" : "disabled"}
    </span>
  );
}

export function PluginsSection() {
  const pluginsQuery = usePluginsQuery();
  const lifecycle = usePluginLifecycleMutation();
  const { toast } = useToast();
  const [pendingUninstall, setPendingUninstall] = useState<Plugin | null>(null);

  function run(plugin: Plugin, action: PluginLifecycleAction): void {
    lifecycle.mutate(
      { id: plugin.id, action },
      {
        onSuccess: () => {
          toast({
            title:
              action === "enable" ? "Plugin enabled" : action === "disable" ? "Plugin disabled" : "Plugin uninstalled",
            description: plugin.name,
            variant: "success",
          });
        },
        onError: (err: unknown) => {
          toast({
            title: "Plugin lifecycle operation failed",
            description: err instanceof ApiError ? err.message : "unexpected error",
            variant: "error",
          });
        },
      },
    );
  }

  const plugins = pluginsQuery.data ?? [];
  const error = pluginsQuery.error;

  return (
    <PlatformSection
      title="Plugins"
      helpTopic="platform-plugins"
      description={
        <>
          Installed SDK extensions and what each one is allowed to touch. Installing happens in the{" "}
          <Link className="text-accent-fg underline" to="/hub">
            Hub
          </Link>
          , where the signature and capability-scope gate lives — there is deliberately no second install path here.
        </>
      }
    >
      {pluginsQuery.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading plugins…</p>}

      {error !== null && (
        <RefusalNotice
          error={error}
          testId="plugins-error"
          forbiddenHint="Listing plugins needs the netRead capability; enabling, disabling and uninstalling need netWrite."
          unavailableHint={
            <>
              The plugin registry is not wired on this daemon, so <code>GET /plugins</code> is not mounted at all.
              That is different from having no plugins installed — this instance cannot answer the question.
            </>
          }
        />
      )}

      {!pluginsQuery.isLoading && error === null && plugins.length === 0 && (
        <p className="text-sm text-slate-600 dark:text-slate-400" data-testid="plugins-empty">
          The plugin registry is wired and reports no installed plugins.
        </p>
      )}

      {plugins.length > 0 && (
        <ul className="space-y-2">
          {plugins.map((plugin) => (
            <li
              key={plugin.id}
              data-testid={`plugin-${plugin.id}`}
              className="rounded-md border border-slate-200 p-3 dark:border-slate-700"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-slate-800 dark:text-slate-100">{plugin.name}</span>
                <span className="font-mono text-xs text-slate-600 dark:text-slate-400">v{plugin.version}</span>
                <EnabledBadge plugin={plugin} />
                <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600 dark:bg-slate-800 dark:text-slate-400">
                  {plugin.transport}
                </span>
                <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600 dark:bg-slate-800 dark:text-slate-400">
                  SDK {plugin.apiVersion}
                </span>
              </div>

              <dl className="mt-2 grid grid-cols-[9rem_1fr] gap-x-3 gap-y-1 text-xs">
                <dt className="text-slate-600 dark:text-slate-400">Capability ceiling</dt>
                <dd data-testid={`plugin-caps-${plugin.id}`}>
                  <ScopeChips names={plugin.capabilities} empty="declares no capabilities" />
                  <span className="mt-0.5 block text-[11px] text-slate-600 dark:text-slate-400">
                    The maximum this plugin may touch. It can stage a changeset but is never itself a way to apply
                    one.
                  </span>
                </dd>

                <dt className="text-slate-600 dark:text-slate-400">Extension points</dt>
                <dd>
                  <ScopeChips names={plugin.extensionPoints} empty="none declared" />
                </dd>

                <dt className="text-slate-600 dark:text-slate-400">Installed</dt>
                <dd className="text-slate-600 dark:text-slate-300">
                  <UnixTime at={plugin.installedAt} /> by {plugin.installedBy}
                </dd>
              </dl>

              <div className="mt-2 flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={lifecycle.isPending}
                  onClick={() => {
                    run(plugin, plugin.enabled ? "disable" : "enable");
                  }}
                  data-testid={`plugin-toggle-${plugin.id}`}
                >
                  {plugin.enabled ? "Disable" : "Enable"}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={lifecycle.isPending}
                  onClick={() => {
                    setPendingUninstall(plugin);
                  }}
                  data-testid={`plugin-uninstall-${plugin.id}`}
                >
                  Uninstall
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <Dialog
        open={pendingUninstall !== null}
        onOpenChange={(open) => {
          if (!open) setPendingUninstall(null);
        }}
      >
        <DialogContent>
          {pendingUninstall && (
            <>
              <DialogTitle>Uninstall {pendingUninstall.name}?</DialogTitle>
              <DialogDescription>
                This tears down any out-of-process subprocess and removes the plugin&rsquo;s extension points. Its
                declared capability scope was{" "}
                {pendingUninstall.capabilities.length > 0 ? pendingUninstall.capabilities.join(", ") : "empty"}.
                Reinstalling later goes through the Hub&rsquo;s signature and trust gate again.
              </DialogDescription>
              <div className="mt-3 flex justify-end gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    setPendingUninstall(null);
                  }}
                >
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  data-testid="plugin-uninstall-confirm"
                  onClick={() => {
                    run(pendingUninstall, "uninstall");
                    setPendingUninstall(null);
                  }}
                >
                  Uninstall
                </Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </PlatformSection>
  );
}
