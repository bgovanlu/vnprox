// SPDX-License-Identifier: Apache-2.0

// T-3904's compiled-ruleset inspector: a read-only, per-node view of the
// nftables ruleset PVE actually compiled and installed, cross-linked to
// the vnprox-authored firewall rule that produced each rule where
// determinable (docs/api.md's "Compiled ruleset (nftables)" section,
// internal/api/nftables.go).
//
// **Not an editor.** No add/delete/edit affordance exists anywhere on
// this page, and none should ever be added — the permanent boundary
// docs/features.md documents ("vnprox ... never installs its own nftables
// ruleset") applies in full here. Every rule shows its best-effort
// extracted match fields; a rule's link back to the rule that produced it
// (`attribution.determined`) renders only when the API itself determined
// one — otherwise `attribution.reason` is shown directly, honestly, next
// to the rule, never a bare "—" that could be mistaken for "nothing to
// say" rather than "could not determine."
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { NftChain, NftRule, NftTable } from "../api/types";
import { ruleEditorDeepLinkPath, type CompiledLinkScope } from "../firewall/compiledLink";
import { scrollIntoViewIfSupported } from "../lib/scrollIntoView";
import { useRouteNodesQuery } from "../routeexplorer/routeQueries";
import { useCompiledRulesetQuery } from "./nftablesQueries";

const inputClass =
  "rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900";

function tableKey(t: NftTable): string {
  return `${t.family} ${t.name}`;
}

/** DOM-id-safe variant of tableKey — used for `id`/`aria-labelledby`,
 * which must never contain whitespace (aria-labelledby splits its value
 * on whitespace to reference more than one id, so an id containing a
 * space silently breaks the association rather than erroring). */
function tableDomId(t: NftTable): string {
  return `table-${t.family}-${t.name}`;
}

function tableChainKey(table: NftTable, chainName: string): string {
  return `${tableKey(table)}|${chainName}`;
}

function chainKey(c: NftChain): string {
  return tableChainKey(c.table, c.name);
}

function ruleMatchSummary(r: NftRule): string {
  const parts: string[] = [];
  if (r.proto) parts.push(r.proto);
  if (r.srcAddr) parts.push(`src ${r.srcAddr}`);
  if (r.dstAddr) parts.push(`dst ${r.dstAddr}`);
  if (r.srcPort) parts.push(`sport ${r.srcPort}`);
  if (r.dstPort) parts.push(`dport ${r.dstPort}`);
  if (r.iifname) parts.push(`iif ${r.iifname}`);
  if (r.oifname) parts.push(`oif ${r.oifname}`);
  if (r.log) parts.push("log");
  return parts.length > 0 ? parts.join(", ") : "(no recognized match fields)";
}

function ruleFocusKey(r: NftRule): string {
  return `${tableChainKey(r.table, r.chain)}|${String(r.handle)}`;
}

interface AttributionCellProps {
  rule: NftRule;
  node: string;
}

function AttributionCell({ rule, node }: AttributionCellProps) {
  const a = rule.attribution;
  if (a.determined && a.scope && a.pos !== undefined) {
    return (
      <Link
        to={ruleEditorDeepLinkPath(a.scope as CompiledLinkScope, a.pos, node)}
        className="text-xs text-accent-700 hover:underline dark:text-accent-400"
      >
        View {a.scope} rule #{a.pos}
      </Link>
    );
  }
  return (
    <span className="text-xs text-slate-600 dark:text-slate-400">
      Not vnprox-authored{a.reason ? ` — ${a.reason}` : ""}
    </span>
  );
}

interface ChainSectionProps {
  chain: NftChain;
  rules: NftRule[];
  focusKey: string | undefined;
  node: string;
}

function ChainSection({ chain, rules, focusKey, node }: ChainSectionProps) {
  return (
    <div className="flex flex-col gap-1">
      <h4 className="flex items-center gap-2 text-xs font-semibold text-slate-700 dark:text-slate-300">
        <span className="font-mono">{chain.name}</span>
        {chain.builtin && (
          <span className="rounded-full bg-slate-200 px-1.5 py-0.5 text-[10px] font-normal text-slate-700 dark:bg-slate-800 dark:text-slate-300">
            PVE built-in
          </span>
        )}
        {chain.hook && (
          <span className="text-[10px] font-normal text-slate-600 dark:text-slate-400">
            hook={chain.hook} policy={chain.policy ?? "—"} priority={chain.priority ?? "—"}
          </span>
        )}
      </h4>
      {rules.length === 0 ? (
        <p className="text-xs text-slate-600 dark:text-slate-400">No rules in this chain.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Handle</TableHead>
              <TableHead>Verdict</TableHead>
              <TableHead>Match</TableHead>
              <TableHead>Comment</TableHead>
              <TableHead>Origin</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((r) => {
              const focused = ruleFocusKey(r) === focusKey;
              return (
                <TableRow
                  key={r.handle}
                  data-focused={focused ? "true" : undefined}
                  className={focused ? "bg-amber-100 ring-2 ring-inset ring-amber-500 dark:bg-amber-900/40" : undefined}
                >
                  <TableCell className="font-mono text-xs">{r.handle}</TableCell>
                  <TableCell className="font-mono text-xs">{r.verdict ?? "—"}</TableCell>
                  <TableCell className="text-xs">{ruleMatchSummary(r)}</TableCell>
                  <TableCell className="text-xs">{r.comment ?? "—"}</TableCell>
                  <TableCell>
                    <AttributionCell rule={r} node={node} />
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

export function CompiledRulesetPage() {
  const [searchParams, setSearchParams] = useSearchParams();

  const nodesQuery = useRouteNodesQuery();
  const nodes = nodesQuery.data?.nodes ?? [];

  const [node, setNode] = useState(() => searchParams.get("node") ?? "");
  const focusScope = searchParams.get("scope") ?? undefined;
  const focusPosRaw = searchParams.get("pos");
  const focusPos = focusPosRaw !== null && Number.isInteger(Number(focusPosRaw)) ? Number(focusPosRaw) : undefined;

  useEffect(() => {
    const first = nodesQuery.data?.nodes[0];
    if (node === "" && first !== undefined) {
      setNode(first);
    }
  }, [node, nodesQuery.data]);

  useEffect(() => {
    const params: Record<string, string> = {};
    if (node) params.node = node;
    setSearchParams(params, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately excludes setSearchParams, which never changes after mount
  }, [node]);

  const rulesetQuery = useCompiledRulesetQuery(node, node !== "");
  const ruleset = rulesetQuery.data;

  const containerRef = useRef<HTMLDivElement>(null);

  // The cross-link's identity is (scope, pos) against a rule's
  // *attribution*, not a compiled identity of its own — resolve the
  // matching compiled rule (if any) once the ruleset has loaded.
  const focusKey = useMemo(() => {
    if (!ruleset || focusScope === undefined || focusPos === undefined) return undefined;
    const match = ruleset.rules.find(
      (r) => r.attribution.determined && r.attribution.scope === focusScope && r.attribution.pos === focusPos,
    );
    return match ? ruleFocusKey(match) : undefined;
  }, [ruleset, focusScope, focusPos]);

  useEffect(() => {
    if (focusKey === undefined) return;
    scrollIntoViewIfSupported(containerRef.current?.querySelector('[data-focused="true"]'), { block: "center" });
  }, [focusKey]);

  const chainsByTable = useMemo(() => {
    if (!ruleset) return [];
    const tables = [...ruleset.tables].sort((a, b) => Number(b.pveAuthored) - Number(a.pveAuthored));
    return tables.map((t) => ({
      table: t,
      chains: ruleset.chains.filter((c) => tableKey(c.table) === tableKey(t)),
    }));
  }, [ruleset]);

  const rulesByChain = useMemo(() => {
    const m = new Map<string, NftRule[]>();
    for (const r of ruleset?.rules ?? []) {
      const k = tableChainKey(r.table, r.chain);
      const list = m.get(k) ?? [];
      list.push(r);
      m.set(k, list);
    }
    return m;
  }, [ruleset]);

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Compiled ruleset"
        description="The nftables ruleset PVE actually compiled and installed on this node — read-only, cross-linked to the vnprox rule that produced each chain where determinable."
        actions={
          <label className="flex items-center gap-2 text-sm">
            Node
            <select
              aria-label="Node"
              value={node}
              onChange={(e) => {
                setNode(e.target.value);
              }}
              className={inputClass}
            >
              {nodes.length === 0 && <option value="">(no nodes)</option>}
              {nodes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        }
      />

      {rulesetQuery.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading compiled ruleset…</p>}
      {rulesetQuery.error && (
        <EmptyState title="Could not load the compiled ruleset" description="Try again in a moment, or pick a different node." />
      )}

      {ruleset?.empty && (
        <EmptyState
          title="No compiled nftables output found on this node"
          description={
            "This can mean either that the firewall is disabled at every scope on this node, or that this node's pve-firewall is running the legacy iptables engine rather than the nftables tech-preview — vnprox's compiled-ruleset inspector reads nftables output only. Check the node's firewall status to tell the two apart."
          }
        />
      )}

      {ruleset && !ruleset.empty && (
        <div ref={containerRef} className="flex flex-col gap-6">
          {chainsByTable.map(({ table, chains }) => (
            <section key={tableKey(table)} aria-labelledby={tableDomId(table)} className="flex flex-col gap-3">
              <h3
                id={tableDomId(table)}
                className="flex items-center gap-2 text-sm font-semibold text-slate-900 dark:text-slate-100"
              >
                <span className="font-mono">{tableKey(table)}</span>
                {!table.pveAuthored && (
                  <span className="rounded-full bg-slate-200 px-1.5 py-0.5 text-[10px] font-normal text-slate-700 dark:bg-slate-800 dark:text-slate-300">
                    not vnprox/PVE firewall output
                  </span>
                )}
              </h3>
              {chains.length === 0 ? (
                <p className="text-xs text-slate-600 dark:text-slate-400">No chains in this table.</p>
              ) : (
                chains.map((c) => (
                  <ChainSection
                    key={chainKey(c)}
                    chain={c}
                    rules={rulesByChain.get(chainKey(c)) ?? []}
                    focusKey={focusKey}
                    node={node}
                  />
                ))
              )}
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
