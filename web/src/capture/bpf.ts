// SPDX-License-Identifier: Apache-2.0

// Pure BPF/pcap filter construction from picker state (T-1302's
// BpfBuilder.tsx) — kept separate from the component so the "given this
// picker state, what filter string comes out" logic is trivially unit
// testable. Every keyword this emits (host/port/tcp/udp/icmp/arp/and) is in
// internal/capture/bpf.go's `bpfKeywords` recognized set, so a filter built
// here always passes the server's syntactic validator — this module never
// tries to be cleverer than that grammar.
export type BpfProtocol = "" | "tcp" | "udp" | "icmp" | "arp";

export interface BpfPickerState {
  protocol: BpfProtocol;
  host: string;
  port: string;
}

export const EMPTY_BPF_PICKER_STATE: BpfPickerState = { protocol: "", host: "", port: "" };

/** Builds a pcap/BPF filter expression from picker state — an empty state
 * yields "" (docs/api.md: "an empty filter is valid — it means capture
 * everything on the scoped interface"). */
export function buildBpfFilter(state: BpfPickerState): string {
  const clauses: string[] = [];
  if (state.protocol) clauses.push(state.protocol);
  const host = state.host.trim();
  if (host) clauses.push(`host ${host}`);
  const port = state.port.trim();
  if (port) clauses.push(`port ${port}`);
  return clauses.join(" and ");
}
