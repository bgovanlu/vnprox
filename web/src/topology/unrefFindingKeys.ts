// SPDX-License-Identifier: Apache-2.0

// Phase 36: the two keys UnrefFindingsBanner needs, in their own module.
//
// They are pure functions over a finding, and they are separate from the
// component so `react-refresh/only-export-components` stays satisfied — but
// mostly so the distinction between them is impossible to miss, because
// conflating the two was a real bug: see remedyActionKey.
import type { Remediation, UnrefFinding } from "../api/types";

/** A finding's identity within this list. Includes `detail` deliberately:
 * `source`/`check`/`nodes` alone are NOT unique — dnsmasq and frr both down
 * on one node produce two findings identical in all three — and a duplicate
 * React key would render one row where there are two. */
export function unrefFindingKey(f: UnrefFinding): string {
  return `${f.source}:${f.check}:${f.nodes.join(",")}:${f.detail}`;
}

/** The key an in-flight action and its result are tracked under.
 *
 * Deliberately NOT unrefFindingKey: that one is derived from the finding as
 * displayed, and the caller running the action only has the remedy in hand.
 * Both sides can derive this one from the remedy's own parameters, so a
 * result cannot land against the wrong row — which is exactly what happened
 * when this was keyed on (check, node): starting dnsmasq showed its error
 * next to frr, because on one node those two findings share every field the
 * old key used. Returns undefined for a remedy that names neither, which
 * cannot be actioned anyway. */
export function remedyActionKey(remedy: Remediation | undefined): string | undefined {
  const node = remedy?.params?.node;
  const service = remedy?.params?.service;
  if (node === undefined || node === "" || service === undefined || service === "") return undefined;
  return `${node}/${service}`;
}
