// SPDX-License-Identifier: Apache-2.0

// Entity kinds internal/metrics.Sampler actually tracks (see
// refMetasFromLinks: PhysNic/Bond/Bridge/VlanIface only — the rest of the
// inventory graph has no interface counters at all). Shared by
// TopologyPage.tsx (which refs to subscribe to for traffic mode) and
// InspectorPanel.tsx (whether to show the Metrics tab at all). Kept in its
// own module (not co-located with the MetricsTab component) so that file
// only exports the component itself — react-refresh/only-export-components.
export const METRICS_KINDS = new Set(["physnic", "bond", "bridge", "vlan"]);
