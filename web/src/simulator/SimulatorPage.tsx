// Tools -> Path simulator (docs/user-guide.md §3's task table: "Why can't
// VM A reach VM B? -> Tools -> Path simulator"; docs/features/firewall.md
// §5). Composes the endpoint pickers, proto/port + service presets, the
// always-honest result panel, and the traced path drawn on an embedded
// topology canvas (T-504 AC2/AC5). All state (both endpoints, proto,
// port) round-trips through the URL (AC4) via urlState.ts, which is also
// what the map's Trace-path action (traceLink.ts) pre-fills through.
import { useEffect, useMemo, useState } from "react";
import { ReactFlowProvider } from "@xyflow/react";
import { useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { useToast } from "../components/Toast";
import type { SimEndpointSpec, VerifyResult } from "../api/types";
import { useFirewallObjectsQuery } from "../firewall/queries";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { computeLayout, type XYPosition } from "../topology/layout";
import { useTopologyQuery } from "../topology/queries";
import { useTopologyStore } from "../topology/store";
import { TopologyCanvas } from "../topology/TopologyCanvas";
import { toFlowElements } from "../topology/toFlowElements";
import { EndpointPicker } from "./EndpointPicker";
import { computePathHighlight, withVerifyHighlight } from "./pathHighlight";
import { useSimulateQuery } from "./queries";
import { ResultPanel } from "./ResultPanel";
import { ANY_PRESET, servicePresetsFromMacros, type ServicePreset } from "./servicePresets";
import { isTraceableEntityKind } from "./traceLink";
import { decodeSimState, encodeSimState, simUrlStatePath, simUrlStateToRequest, type SimUrlState } from "./urlState";
import { VerifyLiveButton } from "./VerifyLiveButton";
import { VerifyPanel } from "./VerifyPanel";
import { HelpAnchor } from "../help/HelpAnchor";

const PROTO_OPTIONS = ["", "tcp", "udp", "icmp"] as const;

/** Which resolved endpoint's ref the blocking rule's enforcement point
 * names, for the map's "blocking point marked" requirement (AC2's sibling
 * concern for deny verdicts). */
function blockingPointRef(result: {
  blockingRule?: { enforcementPoint: string } | undefined;
  src: { ref?: string };
  dst: { ref?: string };
}): string | undefined {
  if (!result.blockingRule) return undefined;
  return result.blockingRule.enforcementPoint === "source-guest-out" ? result.src.ref : result.dst.ref;
}

export function SimulatorPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const initial = useMemo(() => decodeSimState(searchParams), []); // eslint-disable-line react-hooks/exhaustive-deps -- read once, on mount, per AC4/AC5's "paste/pre-fill and run"

  const [src, setSrc] = useState<SimEndpointSpec | undefined>(initial.src);
  const [dst, setDst] = useState<SimEndpointSpec | undefined>(initial.dst);
  const [proto, setProto] = useState<string | undefined>(initial.proto);
  const [port, setPort] = useState<number | undefined>(initial.port);
  // T-806 "Verify live": the most recent live-probe result, if any. Cleared
  // whenever the request tuple changes (a new src/dst/proto/port pick makes
  // any prior live result stale/irrelevant — never show a live result next
  // to a simulated result for a *different* tuple).
  const [verifyResult, setVerifyResult] = useState<VerifyResult | undefined>(undefined);
  const { toast } = useToast();

  // Keep the URL in sync with the in-progress request (replace, not push —
  // every keystroke/pick shouldn't grow browser history) so "Copy link"
  // and a plain page reload both always reflect exactly what's on screen.
  useEffect(() => {
    const state: SimUrlState = { src, dst, proto, port };
    setSearchParams(encodeSimState(state), { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [src, dst, proto, port]);

  const request = useMemo(() => simUrlStateToRequest({ src, dst, proto, port }), [src, dst, proto, port]);
  const { data: result, isFetching, error } = useSimulateQuery(request);

  useEffect(() => {
    setVerifyResult(undefined);
  }, [request]);

  const { data: topology } = useTopologyQuery();
  const { data: fwObjects } = useFirewallObjectsQuery();
  const presets: ServicePreset[] = useMemo(
    () => [ANY_PRESET, ...servicePresetsFromMacros(fwObjects?.macros ?? [])],
    [fwObjects],
  );

  // T-903 command-palette verb: "Simulate path from <entity>" — pre-fills
  // this page's own `src` endpoint from whatever's currently selected on
  // the map (the shared topology store's selection, docs/features/
  // topology.md §2), the same guest-nic-only traceability rule
  // traceLink.ts's map "Trace path from here" action already enforces.
  // Only registered while a traceable entity is actually selected, so the
  // palette never offers a verb that would silently do nothing.
  const selectedRef = useTopologyStore((s) => s.selectedId);
  const selectedNode = useMemo(
    () => topology?.nodes.find((n) => n.id === selectedRef),
    [topology, selectedRef],
  );
  const simulatorPaletteActions = useMemo<PaletteAction[]>(() => {
    if (!selectedNode || !isTraceableEntityKind(selectedNode.kind)) return [];
    return [
      {
        id: `simulate-path-from-${selectedNode.id}`,
        label: `Simulate path from ${selectedNode.label}`,
        hint: "Simulator",
        perform: () => {
          setSrc({ kind: "guest-nic", ref: selectedNode.id });
        },
      },
    ];
  }, [selectedNode]);
  usePaletteActions("simulator", simulatorPaletteActions);

  const [layoutPositions, setLayoutPositions] = useState<Map<string, XYPosition>>(new Map());
  const layoutSignature = topology ? `${String(topology.nodes.length)}:${String(topology.edges.length)}` : "";
  useEffect(() => {
    if (!topology) return;
    let cancelled = false;
    void computeLayout(topology.nodes, topology.edges).then((positions) => {
      if (!cancelled) setLayoutPositions(positions);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutSignature]);

  const pathHighlight = useMemo(() => {
    if (!result) return undefined;
    const base = computePathHighlight(result.hops, topology?.edges ?? [], result.verdict, result.missing, blockingPointRef(result));
    if (!verifyResult) return base;
    // verifyResult.simulated is byte-identical to `result` for the same
    // tuple (see this useEffect above resetting verifyResult on any
    // request change) — its own `src.ref` is the probed source's ref, the
    // node T-806's observed-outcome/divergence marker attaches to.
    return withVerifyHighlight(base, verifyResult.simulated.src.ref, verifyResult.observed.outcome, verifyResult.diverges);
  }, [result, topology, verifyResult]);

  const elements = useMemo(
    () =>
      toFlowElements({
        nodes: topology?.nodes ?? [],
        edges: topology?.edges ?? [],
        expandedGroups: new Set(),
        activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
        layoutPositions,
        manualPositions: {},
        pathHighlight,
      }),
    [topology, layoutPositions, pathHighlight],
  );

  function applyPreset(name: string): void {
    const preset = presets.find((p) => p.name === name);
    if (!preset) return;
    setProto(preset.proto);
    setPort(preset.port);
  }

  function copyLink(): void {
    const path = simUrlStatePath("/tools", { src, dst, proto, port });
    const url = `${window.location.origin}${path}`;
    void navigator.clipboard
      .writeText(url)
      .then(() => {
        toast({ title: "Link copied", description: "Paste it anywhere to reproduce this exact simulation." });
      })
      .catch(() => {
        toast({ title: "Could not copy link", variant: "error" });
      });
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            Path simulator
            <HelpAnchor topic="path-simulator" />
          </h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            "Why can't VM A reach VM B?" — static analysis over configured state (docs/features/firewall.md §5). Every
            result below is labeled Simulated and lists what wasn't (or couldn't be) evaluated.
          </p>
        </div>
        <Button size="sm" variant="secondary" onClick={copyLink}>
          Copy link
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <EndpointPicker label="Source" value={src} onChange={setSrc} topologyNodes={topology?.nodes} />
        <EndpointPicker label="Destination" value={dst} onChange={setDst} topologyNodes={topology?.nodes} />
      </div>

      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs">
          <span className="font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">Protocol</span>
          <select
            aria-label="Protocol"
            value={proto ?? ""}
            onChange={(e) => {
              setProto(e.target.value || undefined);
            }}
            className="h-8 rounded border border-slate-300 bg-transparent px-2 text-sm dark:border-slate-700"
          >
            {PROTO_OPTIONS.map((p) => (
              <option key={p} value={p}>
                {p === "" ? "Any" : p.toUpperCase()}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs">
          <span className="font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">Port</span>
          <input
            aria-label="Port"
            type="number"
            min={0}
            max={65535}
            value={port ?? ""}
            onChange={(e) => {
              const n = Number(e.target.value);
              setPort(e.target.value === "" ? undefined : Number.isFinite(n) ? n : undefined);
            }}
            placeholder="Any"
            className="h-8 w-24 rounded border border-slate-300 bg-transparent px-2 text-sm dark:border-slate-700"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs">
          <span className="font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">Service preset</span>
          <select
            aria-label="Service preset"
            defaultValue=""
            onChange={(e) => {
              if (e.target.value) applyPreset(e.target.value);
              e.target.value = "";
            }}
            className="h-8 rounded border border-slate-300 bg-transparent px-2 text-sm dark:border-slate-700"
          >
            <option value="" disabled>
              Choose a preset…
            </option>
            {presets.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
      </div>

      {!request && (
        <EmptyState title="Pick both endpoints" description="Choose a source and destination above to run a simulation." />
      )}
      {request && isFetching && <p className="text-sm text-slate-400">Simulating…</p>}
      {request && !isFetching && error && (
        <EmptyState
          title="Could not run this simulation"
          description={error instanceof Error ? error.message : "Check the endpoints and try again."}
        />
      )}
      {request && !isFetching && result && (
        <div className="flex flex-col gap-3">
          <ResultPanel result={result} />
          <VerifyLiveButton src={src} request={request} onResult={setVerifyResult} />
          {verifyResult && <VerifyPanel verify={verifyResult} />}
        </div>
      )}

      <div>
        <h3 className="mb-1.5 text-sm font-semibold text-slate-700 dark:text-slate-200">Map</h3>
        <div className="h-[420px] rounded-lg border border-slate-200 dark:border-slate-800">
          {topology && topology.nodes.length > 0 ? (
            <ReactFlowProvider>
              <TopologyCanvas
                elements={elements}
                onNodeClick={() => undefined}
                onNodeHover={() => undefined}
                onNodeDragStop={() => undefined}
                onPaneClick={() => undefined}
              />
            </ReactFlowProvider>
          ) : (
            <div className="flex h-full items-center justify-center">
              <EmptyState title="No map data yet" description="Waiting for the topology to load." />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
