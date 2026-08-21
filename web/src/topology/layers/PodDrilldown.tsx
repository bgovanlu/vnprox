// T-1502: the "why is this pod unreachable" panel — opened when a
// Kubernetes-layer overlay entity (a pod, its pod-CIDR region, or a
// service) is selected on the map. Traces the correlated k8s node's PVE
// guest down through the real topology graph (k8sUnderlay.ts's
// computePodUnderlayChain) alongside the pod/service's own k8s-side facts,
// so the underlay half (pod -> node-guest -> bridge -> bond) shows up
// right next to the k8s-side view nobody else combines.
//
// A small, non-modal floating panel, mirroring FlowPairPanel.tsx's
// convention exactly (dl field grid, Close button, onClose prop) — placed
// bottom-center (`fixed bottom-4 left-1/2 -translate-x-1/2`) rather than
// either existing panel's corner: InspectorStack.tsx already claims
// bottom-right and FlowPairPanel.tsx claims top-right, and Sidebar.tsx
// occupies the left edge in normal document flow (not `fixed`, but still
// visually busy down to its w-16/w-56 column) — bottom-center is the one
// remaining spot that collides with none of them.
import type { K8sOverlay, K8sPodSummary, K8sServiceInfo, TopologyEdge, TopologyNode } from "../../api/types";
import { computePodUnderlayChain, type UnderlayPath } from "./k8sUnderlay";
import type { K8sSelection } from "./k8sOverlay";

export interface PodDrilldownProps {
  selection: K8sSelection;
  /** The selection's own cluster overlay — undefined while still loading. */
  overlay: K8sOverlay | undefined;
  topologyNodes: readonly TopologyNode[];
  topologyEdges: readonly TopologyEdge[];
  onClose: () => void;
}

function podsOnNode(overlay: K8sOverlay, node: string): K8sPodSummary[] {
  return overlay.pods.filter((p) => p.node === node);
}

function findService(overlay: K8sOverlay, namespace: string, name: string): K8sServiceInfo | undefined {
  return overlay.services.find((s) => s.namespace === namespace && s.name === name);
}

function findPod(overlay: K8sOverlay, namespace: string, name: string): K8sPodSummary | undefined {
  return overlay.pods.find((p) => p.namespace === namespace && p.name === name);
}

/** The k8s-node name this selection's underlay chain should be traced
 * from — undefined for a service (a service isn't pinned to one node). */
function k8sNodeForSelection(selection: K8sSelection, overlay: K8sOverlay): string | undefined {
  if (selection.kind === "pod-cidr") return selection.node;
  if (selection.kind === "pod") return findPod(overlay, selection.namespace, selection.name)?.node;
  return undefined;
}

function UnderlayChain({ paths }: { paths: UnderlayPath[] }) {
  if (paths.length === 0) {
    return (
      <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
        No rendered guest-NIC path for this guest — it may be filtered out or not yet collected.
      </p>
    );
  }
  return (
    <div className="mt-2 flex flex-col gap-2">
      {paths.map((path) => (
        <ol key={path.nicId} className="flex flex-wrap items-center gap-1 text-xs">
          {path.hops.map((hop, i) => (
            <li key={hop.id} className="flex items-center gap-1">
              {i > 0 && <span className="text-slate-600 dark:text-slate-400">&rarr;</span>}
              <span
                className="rounded border border-slate-200 bg-slate-50 px-1.5 py-0.5 font-mono dark:border-slate-700 dark:bg-slate-800"
                title={hop.id}
              >
                {hop.label}
              </span>
            </li>
          ))}
        </ol>
      ))}
    </div>
  );
}

export function PodDrilldown({ selection, overlay, topologyNodes, topologyEdges, onClose }: PodDrilldownProps) {
  const title =
    selection.kind === "service" ? "Service" : selection.kind === "pod" ? "Pod" : "Pod network";

  return (
    <div
      role="region"
      aria-label="Pod drilldown"
      className="fixed bottom-4 left-1/2 z-30 w-[28rem] max-w-full -translate-x-1/2 rounded-lg border border-slate-200 bg-white/95 p-4 shadow-xl backdrop-blur dark:border-slate-700 dark:bg-slate-900/95"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold">{title}</h3>
        <button
          type="button"
          aria-label="Close"
          onClick={onClose}
          className="rounded px-1.5 text-slate-600 dark:text-slate-400 hover:bg-slate-100 hover:text-slate-700 dark:hover:bg-slate-800 dark:hover:text-slate-200"
        >
          ×
        </button>
      </div>

      {!overlay ? (
        <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">Loading…</p>
      ) : (
        <PodDrilldownBody selection={selection} overlay={overlay} topologyNodes={topologyNodes} topologyEdges={topologyEdges} />
      )}
    </div>
  );
}

function PodDrilldownBody({
  selection,
  overlay,
  topologyNodes,
  topologyEdges,
}: {
  selection: K8sSelection;
  overlay: K8sOverlay;
  topologyNodes: readonly TopologyNode[];
  topologyEdges: readonly TopologyEdge[];
}) {
  if (selection.kind === "service") {
    const svc = findService(overlay, selection.namespace, selection.name);
    if (!svc) {
      return <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">Service no longer present on this poll.</p>;
    }
    const finding = overlay.nodePortFindings?.find(
      (f) => f.namespace === selection.namespace && f.service === selection.name,
    );
    return (
      <>
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
          <dt className="text-slate-500 dark:text-slate-400">Name</dt>
          <dd className="font-mono">
            {svc.namespace}/{svc.name}
          </dd>
          <dt className="text-slate-500 dark:text-slate-400">Type</dt>
          <dd>{svc.type}</dd>
          <dt className="text-slate-500 dark:text-slate-400">ClusterIP</dt>
          <dd className="font-mono">{svc.clusterIp ?? "(headless)"}</dd>
          <dt className="text-slate-500 dark:text-slate-400">Ports</dt>
          <dd>
            {(svc.ports ?? [])
              .map((p) => `${String(p.port)}${p.nodePort ? `:${String(p.nodePort)}` : ""}/${p.protocol}`)
              .join(", ") || "—"}
          </dd>
        </dl>
        {finding && (
          <p className="mt-2 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
            {finding.detail}
          </p>
        )}
        <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
          A service isn&rsquo;t pinned to one node — select a pod on this service&rsquo;s pod network to see its
          underlay path.
        </p>
      </>
    );
  }

  const k8sNode = k8sNodeForSelection(selection, overlay);
  const correlation = k8sNode ? overlay.nodes.find((n) => n.k8sNode === k8sNode) : undefined;
  const pod = selection.kind === "pod" ? findPod(overlay, selection.namespace, selection.name) : undefined;
  const pods = k8sNode ? podsOnNode(overlay, k8sNode) : [];

  return (
    <>
      <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
        {pod && (
          <>
            <dt className="text-slate-500 dark:text-slate-400">Pod</dt>
            <dd className="font-mono">
              {pod.namespace}/{pod.name}
            </dd>
            <dt className="text-slate-500 dark:text-slate-400">Pod IP</dt>
            <dd className="font-mono">{pod.podIp ?? "—"}</dd>
            <dt className="text-slate-500 dark:text-slate-400">Phase</dt>
            <dd>{pod.phase ?? "unknown"}</dd>
          </>
        )}
        <dt className="text-slate-500 dark:text-slate-400">k8s node</dt>
        <dd className="font-mono">{k8sNode ?? "—"}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Correlated guest</dt>
        <dd className="font-mono">{correlation?.matched ? (correlation.guestRef ?? "—") : "unmatched"}</dd>
        {selection.kind === "pod-cidr" && (
          <>
            <dt className="text-slate-500 dark:text-slate-400">Pods on this node</dt>
            <dd>{pods.length}</dd>
          </>
        )}
      </dl>

      <h4 className="mt-3 text-xs font-semibold text-slate-600 dark:text-slate-300">Underlay path</h4>
      {correlation?.matched && correlation.guestRef ? (
        <UnderlayChain paths={computePodUnderlayChain(topologyNodes, topologyEdges, correlation.guestRef)} />
      ) : (
        <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
          This k8s node has no correlated PVE guest (unmatched) — the underlay path can&rsquo;t be shown.
        </p>
      )}
    </>
  );
}
