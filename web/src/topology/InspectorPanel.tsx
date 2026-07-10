import * as RadixTabs from "@radix-ui/react-tabs";
import clsx from "clsx";
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from "../components/Drawer";
import { EmptyState } from "../components/EmptyState";
import { fieldRows } from "./fields";
import { useInventoryDetailQuery } from "./queries";

export interface InspectorPanelProps {
  /** A real inventory ref (never a guest-group id — see TopologyPage's
   * click handler, which routes guest-group clicks to expand/collapse
   * instead of ever setting this). undefined closes the panel. */
  selectedRef: string | undefined;
  onClose: () => void;
  onSelectRelated: (ref: string) => void;
}

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-xs font-medium text-slate-500 data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:text-slate-400 dark:data-[state=active]:text-accent-400";

/**
 * The click-to-inspect panel (docs/features/topology.md §2: "inspector
 * panel: normalized fields, live status, raw source, related entities").
 * "Raw source" is GET /inventory/{ref}'s `rawSource` map — the verbatim
 * interfaces(5) stanza / pretty-printed PVE API object / observed-state
 * JSON per contributing source (docs/api.md). Per-field provenance (which
 * source won each resolved field) stays visible on its own tab alongside
 * it: rawSource shows what each source said, provenance shows who won.
 */
export function InspectorPanel({ selectedRef, onClose, onSelectRelated }: InspectorPanelProps) {
  const { data, isLoading, isError } = useInventoryDetailQuery(selectedRef);
  const rawSourceEntries = Object.entries(data?.rawSource ?? {});

  return (
    <Drawer open={selectedRef !== undefined} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DrawerContent side="right" aria-describedby="inspector-description">
        <DrawerTitle>{data?.label ?? "Inspector"}</DrawerTitle>
        <DrawerDescription id="inspector-description">
          {data ? `${data.kind} on ${data.node || "cluster"}` : "Entity detail"}
        </DrawerDescription>

        {isLoading && <p className="mt-4 text-sm text-slate-400">Loading…</p>}
        {isError && (
          <EmptyState className="mt-4" title="Could not load this entity" description="It may have just been removed." />
        )}

        {data && (
          <RadixTabs.Root defaultValue="fields" className="mt-4 flex flex-1 flex-col">
            <RadixTabs.List className="flex gap-1 border-b border-slate-200 dark:border-slate-700">
              <RadixTabs.Trigger value="fields" className={tabTriggerClass}>
                Fields
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="raw" className={tabTriggerClass}>
                Raw source
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="provenance" className={tabTriggerClass}>
                Provenance
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="related" className={tabTriggerClass}>
                Related ({data.related.length})
              </RadixTabs.Trigger>
            </RadixTabs.List>

            <RadixTabs.Content value="fields" className="mt-3 flex-1 overflow-y-auto">
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
                {fieldRows(data.fields).map(([k, v]) => (
                  <div className="contents" key={k}>
                    <dt className="text-slate-400">{k}</dt>
                    <dd className="break-all text-slate-700 dark:text-slate-200">{v}</dd>
                  </div>
                ))}
              </dl>
            </RadixTabs.Content>

            <RadixTabs.Content value="raw" className="mt-3 flex-1 overflow-y-auto">
              {rawSourceEntries.length === 0 ? (
                <p className="text-xs text-slate-400">
                  No raw source retained for this entity — none of its contributing sources kept original
                  config text (common for purely observed entities).
                </p>
              ) : (
                <div className="space-y-3">
                  {rawSourceEntries.map(([source, text]) => (
                    <section key={source}>
                      <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">{source}</h3>
                      <pre className="overflow-x-auto rounded border border-slate-200 bg-slate-50 p-2 font-mono text-[11px] leading-snug text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200">
                        {text}
                      </pre>
                    </section>
                  ))}
                </div>
              )}
            </RadixTabs.Content>

            <RadixTabs.Content value="provenance" className="mt-3 flex-1 overflow-y-auto">
              <p className="mb-2 text-xs text-slate-400">
                Resolved-field provenance: which poll source won each field, and any disagreeing values.
              </p>
              <ul className="space-y-2 text-xs">
                {Object.entries(data.provenance).map(([field, prov]) => (
                  <li key={field} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                    <div className="font-medium text-slate-700 dark:text-slate-200">
                      {field} <span className="font-normal text-slate-400">← {prov.owner}</span>
                    </div>
                    {prov.conflicts && prov.conflicts.length > 0 && (
                      <ul className="mt-1 space-y-0.5 pl-3 text-amber-600 dark:text-amber-400">
                        {prov.conflicts.map((c, i) => (
                          <li key={i}>
                            {c.source}: {c.value}
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                ))}
                {Object.keys(data.provenance).length === 0 && (
                  <li className="text-slate-400">No per-field provenance recorded.</li>
                )}
              </ul>
            </RadixTabs.Content>

            <RadixTabs.Content value="related" className="mt-3 flex-1 overflow-y-auto">
              <ul className="space-y-1 text-xs">
                {data.related.map((r) => (
                  <li key={`${r.ref}-${r.edgeKind}-${r.direction}`}>
                    <button
                      type="button"
                      onClick={() => {
                        onSelectRelated(r.ref);
                      }}
                      className={clsx(
                        "flex w-full items-center justify-between rounded px-2 py-1 text-left",
                        "hover:bg-slate-100 dark:hover:bg-slate-800",
                      )}
                    >
                      <span className="truncate text-slate-700 dark:text-slate-200">{r.ref}</span>
                      <span className="shrink-0 text-slate-400">
                        {r.edgeKind} ({r.direction})
                      </span>
                    </button>
                  </li>
                ))}
                {data.related.length === 0 && <li className="text-slate-400">No related entities.</li>}
              </ul>
            </RadixTabs.Content>
          </RadixTabs.Root>
        )}
      </DrawerContent>
    </Drawer>
  );
}
