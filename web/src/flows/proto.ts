// SPDX-License-Identifier: Apache-2.0

// Client-side mirror of internal/flow/record.go's protoNames table — kept
// as its own tiny module so both reducer.ts (filter matching) and
// FlowExplorer.tsx (display) share one source of truth instead of two
// hand-copied maps drifting apart.
export const PROTO_NAME_TO_NUMBER: Record<string, number> = {
  icmp: 1,
  tcp: 6,
  udp: 17,
  icmpv6: 58,
  sctp: 132,
};

const PROTO_NUMBER_TO_NAME: Record<number, string> = Object.fromEntries(
  Object.entries(PROTO_NAME_TO_NUMBER).map(([name, num]) => [num, name]),
);

/** Mirrors ProtoName (Go): the lowercase conventional name for a proto
 * number, or its decimal string when unrecognized. */
export function protoName(proto: number): string {
  return PROTO_NUMBER_TO_NAME[proto] ?? String(proto);
}
