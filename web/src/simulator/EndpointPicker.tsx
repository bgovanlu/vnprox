// One endpoint (source or destination) of a simulated flow (docs/features/
// firewall.md §5: "Endpoints: guest NIC, arbitrary IP, or external/WAN").
// Guest NIC search reuses the same GET /inventory/search client the map's
// spotlight search already uses (topology/queries.ts's useSearchQuery),
// filtered to guest-nic results only (the only endpoint kind a guest
// picker can resolve, per docs/api.md's EndpointSpec contract).
import { useEffect, useState } from "react";
import clsx from "clsx";
import type { SimEndpointKind, SimEndpointSpec, TopologyNode } from "../api/types";
import { useSearchQuery } from "../topology/queries";
import { describeIpSubnetContext } from "./ipSubnetContext";

export interface EndpointPickerProps {
  label: string;
  value: SimEndpointSpec | undefined;
  onChange: (spec: SimEndpointSpec | undefined) => void;
  /** Topology nodes for the IP endpoint's "subnet context" hint (see
   * ipSubnetContext.ts) — optional so this component is usable/testable
   * without a full topology fetch in hand. */
  topologyNodes?: readonly TopologyNode[];
}

const KIND_TABS: { kind: SimEndpointKind; short: string }[] = [
  { kind: "guest-nic", short: "Guest NIC" },
  { kind: "ip", short: "IP address" },
  { kind: "external", short: "External" },
];

export function EndpointPicker({ label, value, onChange, topologyNodes = [] }: EndpointPickerProps) {
  // `mode` is its own state (not derived straight from `value.kind`)
  // because a user must be able to switch tabs — see the guest-nic search
  // box or the IP input — *before* a value exists yet (value stays
  // undefined until a guest NIC is picked or an IP is committed). It stays
  // in sync with `value` whenever the caller sets one externally (URL
  // restore on mount, a Trace-path pre-fill, or Change/paste).
  const [mode, setMode] = useState<SimEndpointKind>(value?.kind ?? "guest-nic");
  const [query, setQuery] = useState("");
  // Only used for the guest-nic chip's display label — SimEndpointSpec
  // itself carries no label, just the ref the API needs.
  const [selectedLabel, setSelectedLabel] = useState<string | undefined>(undefined);
  const [ipDraft, setIpDraft] = useState(value?.kind === "ip" ? (value.ip ?? "") : "");

  useEffect(() => {
    if (value && value.kind !== mode) {
      setMode(value.kind);
      if (value.kind === "ip") {
        setIpDraft(value.ip ?? "");
      }
    }
    // Only re-sync when the caller hands in a *new* value object (e.g. a
    // fresh Trace-path prefill or URL restore) — not on our own `mode`
    // state changes, which would fight the user's in-progress tab click.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const { data: searchData, isFetching } = useSearchQuery(mode === "guest-nic" && !value ? query : "");
  const results = (searchData?.results ?? []).filter((r) => r.kind === "guest-nic");

  function switchMode(next: SimEndpointKind): void {
    if (next === mode) return;
    setMode(next);
    setQuery("");
    setSelectedLabel(undefined);
    if (next === "external") {
      onChange({ kind: "external" });
    } else if (next === "ip") {
      setIpDraft("");
      onChange(undefined);
    } else {
      onChange(undefined);
    }
  }

  function commitIp(next: string): void {
    const trimmed = next.trim();
    onChange(trimmed ? { kind: "ip", ip: trimmed } : undefined);
  }

  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        {label}
      </legend>
      <div role="radiogroup" aria-label={`${label} endpoint kind`} className="flex gap-1">
        {KIND_TABS.map((t) => (
          <button
            key={t.kind}
            type="button"
            role="radio"
            aria-checked={mode === t.kind}
            onClick={() => {
              switchMode(t.kind);
            }}
            className={clsx(
              "rounded px-2 py-1 text-xs font-medium",
              mode === t.kind
                ? "bg-accent-600 text-white"
                : "bg-slate-100 text-slate-600 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700",
            )}
          >
            {t.short}
          </button>
        ))}
      </div>

      {mode === "guest-nic" && (
        <div className="flex flex-col gap-1">
          {value?.kind === "guest-nic" ? (
            <div className="flex items-center justify-between gap-2 rounded border border-slate-300 px-2 py-1.5 text-sm dark:border-slate-700">
              <span className="truncate font-mono text-xs">{selectedLabel ?? value.ref}</span>
              <button
                type="button"
                className="shrink-0 text-xs text-accent-600 hover:underline dark:text-accent-400"
                onClick={() => {
                  onChange(undefined);
                  setSelectedLabel(undefined);
                }}
              >
                Change
              </button>
            </div>
          ) : (
            <>
              <input
                type="text"
                aria-label={`${label} guest NIC search`}
                placeholder="Search guest name, MAC, IP…"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                }}
                className="w-full rounded border border-slate-300 bg-transparent px-2 py-1.5 text-sm outline-none focus:border-accent-500 dark:border-slate-700"
              />
              {query.trim() !== "" && (
                <ul className="max-h-40 overflow-y-auto rounded border border-slate-200 text-sm dark:border-slate-700">
                  {isFetching && <li className="px-2 py-1 text-slate-400">Searching…</li>}
                  {!isFetching && results.length === 0 && (
                    <li className="px-2 py-1 text-slate-400">No matching guest NICs.</li>
                  )}
                  {results.map((r) => (
                    <li key={r.ref}>
                      <button
                        type="button"
                        onClick={() => {
                          onChange({ kind: "guest-nic", ref: r.ref });
                          setSelectedLabel(`${r.label} (${r.node})`);
                          setQuery("");
                        }}
                        className="flex w-full items-center justify-between gap-2 px-2 py-1 text-left hover:bg-slate-100 dark:hover:bg-slate-800"
                      >
                        <span className="truncate">{r.label}</span>
                        <span className="shrink-0 text-xs text-slate-400">{r.node}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </div>
      )}

      {mode === "ip" && (
        <div className="flex flex-col gap-1">
          <input
            type="text"
            aria-label={`${label} IP address`}
            placeholder="10.0.0.5"
            value={ipDraft}
            onChange={(e) => {
              setIpDraft(e.target.value);
            }}
            onBlur={() => {
              commitIp(ipDraft);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                commitIp(ipDraft);
              }
            }}
            className="w-full rounded border border-slate-300 bg-transparent px-2 py-1.5 font-mono text-sm outline-none focus:border-accent-500 dark:border-slate-700"
          />
          {ipDraft.trim() !== "" && (
            <p className="text-xs text-slate-400">{describeIpSubnetContext(ipDraft.trim(), topologyNodes)}</p>
          )}
        </div>
      )}

      {mode === "external" && (
        <p className="text-xs text-slate-400">Represents traffic to/from outside the cluster (the WAN/internet).</p>
      )}
    </fieldset>
  );
}
