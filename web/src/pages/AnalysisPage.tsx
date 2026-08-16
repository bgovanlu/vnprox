// The Analysis screen (T-3004): five read-mostly analyses that had a
// backend and no way in — failure simulation, capacity history export, QoS
// shaping, PBS backup paths, and IPv6 segments. WAN health is the sixth of
// the set and lives on the Edge page instead, because "how does traffic
// leave, and does it get anywhere" is one question and the Edge cockpit
// already asks the first half.
//
// What these five have in common is the shape of the answer: each one reads
// something vnprox already knows and turns it into a judgement about the
// cluster, rather than showing configuration. That is why they share a
// screen rather than being scattered across the pages whose entities they
// happen to name.
//
// Exactly one of them writes anything, and it does not write directly: a QoS
// edit stages a `qos.shape.*` op into the change drawer and stops there.
import { CapacityExportPanel } from "../analysis/CapacityExportPanel";
import { PbsPanel } from "../analysis/PbsPanel";
import { QosShapesPanel } from "../analysis/QosShapesPanel";
import { SpofPanel } from "../analysis/SpofPanel";
import { HelpAnchor } from "../help/HelpAnchor";
import { IPv6SegmentsPanel } from "../ipv6/IPv6SegmentsPanel";

export function AnalysisPage() {
  return (
    <div className="flex h-full flex-col gap-6 overflow-y-auto">
      <div>
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          Analysis
          <HelpAnchor topic="analysis-page" />
        </h1>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          What breaks if something dies, where capacity is heading, what is shaped, where backups actually flow, and
          what IPv6 is really doing. Read-mostly: the one editable thing here is QoS shaping, and it stages a changeset
          like every other edit in vnprox.
        </p>
      </div>

      <SpofPanel />
      <hr className="border-slate-200 dark:border-slate-800" />
      <CapacityExportPanel />
      <hr className="border-slate-200 dark:border-slate-800" />
      <QosShapesPanel />
      <hr className="border-slate-200 dark:border-slate-800" />
      <PbsPanel />
      <hr className="border-slate-200 dark:border-slate-800" />
      <IPv6SegmentsPanel />
    </div>
  );
}
