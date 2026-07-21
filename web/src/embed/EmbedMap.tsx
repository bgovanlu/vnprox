// EmbedMap (T-1706): a read-only projection of GET /topology for embedding.
// It reuses the same topology read API the full map uses, but renders a
// static, grouped, non-interactive view — no context menus, no inspector
// mutations, no changeset affordances, no drag/edit surface. Every mutation
// entry point is absent at the component level (the full interactive
// editor's ContextMenu/InspectorPanel/edit chrome is simply not mounted),
// not hidden with CSS, so an embed can never become a write surface.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchTopology } from "../api/topology";
import type { EntityStatus, TopologyNode } from "../api/types";

const STATUS_DOT: Record<EntityStatus, string> = {
  ok: "bg-emerald-500",
  down: "bg-red-500",
  degraded: "bg-amber-500",
  unknown: "bg-slate-400",
};

const STATUS_LABEL: Record<EntityStatus, string> = {
  ok: "OK",
  down: "Down",
  degraded: "Degraded",
  unknown: "Unknown",
};

function groupLabel(nodeGroup: string): string {
  return nodeGroup === "" ? "Cluster-wide (SDN)" : nodeGroup;
}

export function EmbedMap() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["embed", "topology"],
    queryFn: () => fetchTopology(),
  });

  const groups = useMemo(() => {
    const byGroup = new Map<string, TopologyNode[]>();
    for (const n of data?.nodes ?? []) {
      const list = byGroup.get(n.nodeGroup) ?? [];
      list.push(n);
      byGroup.set(n.nodeGroup, list);
    }
    return [...byGroup.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [data]);

  if (isLoading) {
    return <p className="text-sm text-slate-500">Loading topology…</p>;
  }
  if (error) {
    return (
      <p className="text-sm text-red-600 dark:text-red-400" data-testid="embed-map-error">
        Could not load the topology.
      </p>
    );
  }
  if (groups.length === 0) {
    return <p className="text-sm text-slate-500">No topology entities to display.</p>;
  }

  return (
    <div className="flex flex-col gap-4" data-testid="embed-map">
      {groups.map(([group, nodes]) => (
        <section key={group} className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
          <h2 className="mb-2 text-sm font-semibold">{groupLabel(group)}</h2>
          <ul className="flex flex-col gap-1">
            {nodes.map((n) => (
              <li key={n.id} className="flex items-center gap-2 text-sm">
                <span
                  aria-hidden
                  className={`h-2.5 w-2.5 shrink-0 rounded-full ${STATUS_DOT[n.status]}`}
                  title={STATUS_LABEL[n.status]}
                />
                <span className="text-slate-500">{n.kind}</span>
                <span className="font-medium">{n.label}</span>
                {n.badges.map((b) => (
                  <span
                    key={b}
                    className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                  >
                    {b}
                  </span>
                ))}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
