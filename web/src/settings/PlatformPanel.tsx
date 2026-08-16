// The Platform panel (T-3003): the eleven Settings-shaped routes that had no
// client at all — tokens, webhooks, plugin lifecycle, and `doctor --live`.
//
// One route rather than four, because these are four sections of one
// question ("what is this daemon carrying, and is it healthy?") and because
// three of the four are small. Each section owns its own queries, its own
// refusal rendering and its own help topic, so they degrade independently: a
// daemon with no plugin registry still shows tokens, and a session without
// the audit capability still sees everything except the self-check.
import { Link } from "react-router-dom";
import { TokensSection } from "./TokensSection";
import { WebhooksSection } from "./WebhooksSection";
import { PluginsSection } from "./PluginsSection";
import { DoctorLiveSection } from "./DoctorLiveSection";

export function PlatformPanel() {
  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <header>
        <h1 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Platform</h1>
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
          Automation credentials, event delivery, installed extensions, and the daemon&rsquo;s own live self-check.
          Each section states what it could not check as clearly as what it could.{" "}
          <Link className="text-accent-700 underline dark:text-accent-400" to="/settings">
            Back to Settings
          </Link>
          .
        </p>
      </header>

      <TokensSection />
      <WebhooksSection />
      <PluginsSection />
      <DoctorLiveSection />
    </div>
  );
}
