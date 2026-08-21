// T-2005's Settings section: per-category push opt-in for this device, and
// a "Your devices" list to review/revoke every device subscribed (not just
// this one). Follows SettingsPage.tsx's existing Section/Row pattern
// rather than a separate page — this is a handful of controls, not a
// surface that needs its own route.
import { useEffect, useMemo, useState } from "react";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import {
  ALL_PUSH_CATEGORIES,
  type PushCategory,
  type PushSubscriptionSummary,
} from "../api/push";
import { getPushBrowserStatus, isPushSupported, requestPushSubscription, unsubscribeBrowserPush } from "./browserPush";
import { guessDeviceLabel } from "./deviceLabel";
import { clearOwnSubscriptionId, getOwnSubscriptionId, setOwnSubscriptionId } from "./ownSubscriptionId";
import {
  useCreatePushSubscriptionMutation,
  useDeletePushSubscriptionMutation,
  usePushSubscriptionsQuery,
  useVapidPublicKeyQuery,
} from "./queries";

const CATEGORY_LABELS: Record<PushCategory, { title: string; detail: string }> = {
  critical: { title: "Critical findings", detail: "A new error-severity finding appears." },
  awaitingConfirm: { title: "Awaiting-confirm changesets", detail: "A changeset applied and needs a confirm before its window closes." },
  drift: { title: "Configuration drift", detail: "Something changed outside vnprox." },
};

function formatTimestamp(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

export function PushSettingsSection() {
  const { toast } = useToast();
  // Computed once (not on every render): whether push is supported at all
  // is a fact about this browser for the lifetime of the tab, and
  // recomputing it per render served no purpose beyond making an
  // effect-driven state update (below) re-derive a stale answer on the
  // very next render — exactly the kind of render/effect feedback loop
  // T-2108's HistoryTimeline postmortem (planning/tasks/phase-20.md's
  // T-2003-bug-01 resolution) is the standing reminder to avoid.
  const supported = useMemo(() => isPushSupported(), []);
  const { data: vapidKey } = useVapidPublicKeyQuery();
  const { data: subscriptions } = usePushSubscriptionsQuery();
  const createMutation = useCreatePushSubscriptionMutation();
  const deleteMutation = useDeletePushSubscriptionMutation();

  const [selected, setSelected] = useState<Set<PushCategory>>(() => new Set(["critical", "awaitingConfirm"]));
  const [thisDeviceSubscribed, setThisDeviceSubscribed] = useState(false);
  const [checkingStatus, setCheckingStatus] = useState(true);

  useEffect(() => {
    let cancelled = false;
    if (!supported) {
      setCheckingStatus(false);
      return;
    }
    void getPushBrowserStatus().then((status) => {
      if (!cancelled) {
        setThisDeviceSubscribed(status.subscription !== null);
        setCheckingStatus(false);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [supported]);

  function toggleCategory(cat: PushCategory): void {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(cat)) next.delete(cat);
      else next.add(cat);
      return next;
    });
  }

  async function handleEnable(): Promise<void> {
    if (!vapidKey) return;
    if (selected.size === 0) {
      toast({ title: "Choose at least one category", variant: "error" });
      return;
    }
    try {
      const sub = await requestPushSubscription(vapidKey);
      if (!sub) {
        toast({ title: "Notification permission was not granted", description: "Allow notifications in the browser to enable push.", variant: "error" });
        return;
      }
      const created = await createMutation.mutateAsync({
        subscription: sub.toJSON() as { endpoint: string; keys: { p256dh: string; auth: string } },
        categories: [...selected],
        deviceLabel: guessDeviceLabel(navigator.userAgent),
      });
      setOwnSubscriptionId(created.id);
      setThisDeviceSubscribed(true);
      toast({ title: "Push enabled on this device", variant: "success" });
    } catch (err) {
      toast({ title: "Could not enable push", description: err instanceof Error ? err.message : undefined, variant: "error" });
    }
  }

  async function handleDisableThisDevice(): Promise<void> {
    const ownId = getOwnSubscriptionId();
    try {
      if (ownId) {
        await deleteMutation.mutateAsync(ownId);
      }
      await unsubscribeBrowserPush();
      clearOwnSubscriptionId();
      setThisDeviceSubscribed(false);
      toast({ title: "Push disabled on this device", variant: "success" });
    } catch (err) {
      toast({ title: "Could not disable push", description: err instanceof Error ? err.message : undefined, variant: "error" });
    }
  }

  async function handleRevoke(sub: PushSubscriptionSummary): Promise<void> {
    try {
      await deleteMutation.mutateAsync(sub.id);
      if (sub.id === getOwnSubscriptionId()) {
        await unsubscribeBrowserPush();
        clearOwnSubscriptionId();
        setThisDeviceSubscribed(false);
      }
      toast({ title: "Device revoked", variant: "success" });
    } catch (err) {
      toast({ title: "Could not revoke device", description: err instanceof Error ? err.message : undefined, variant: "error" });
    }
  }

  if (!supported) {
    return (
      <p className="text-sm text-slate-500 dark:text-slate-400">
        This browser does not support push notifications.
      </p>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <HelpAnchor topic="push-notifications" />
      </div>
      <fieldset className="space-y-2">
        <legend className="text-xs font-medium text-slate-600 dark:text-slate-300">Notify me about</legend>
        {ALL_PUSH_CATEGORIES.map((cat) => (
          <label key={cat} className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={selected.has(cat)}
              disabled={thisDeviceSubscribed}
              onChange={() => {
                toggleCategory(cat);
              }}
            />
            <span>
              <span className="font-medium text-slate-700 dark:text-slate-200">{CATEGORY_LABELS[cat].title}</span>
              <span className="block text-xs text-slate-500 dark:text-slate-400">{CATEGORY_LABELS[cat].detail}</span>
            </span>
          </label>
        ))}
      </fieldset>

      <div>
        {checkingStatus ? (
          <p className="text-sm text-slate-600 dark:text-slate-400">Checking this device…</p>
        ) : thisDeviceSubscribed ? (
          <Button size="sm" variant="secondary" onClick={() => void handleDisableThisDevice()}>
            Disable push on this device
          </Button>
        ) : (
          <Button size="sm" onClick={() => void handleEnable()} disabled={!vapidKey}>
            Enable push on this device
          </Button>
        )}
      </div>

      {subscriptions && subscriptions.length > 0 && (
        <div>
          <h3 className="text-xs font-medium text-slate-600 dark:text-slate-300">Your devices</h3>
          <ul className="mt-2 divide-y divide-slate-100 text-sm dark:divide-slate-800">
            {subscriptions.map((sub) => (
              <li key={sub.id} className="flex flex-wrap items-center justify-between gap-2 py-1.5">
                <div>
                  <span className="font-medium text-slate-700 dark:text-slate-200">{sub.deviceLabel ?? "Unlabeled device"}</span>
                  <span className="ml-2 text-xs text-slate-500 dark:text-slate-400">
                    {sub.categories.join(", ")} · added {formatTimestamp(sub.createdAt)}
                  </span>
                </div>
                <Button size="sm" variant="ghost" onClick={() => void handleRevoke(sub)}>
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
