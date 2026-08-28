// SPDX-License-Identifier: Apache-2.0

// T-2802: the guided tour's script, written against T-2801's demo dataset.
//
// The card asks for "a scripted tour covering the six surfaces the datasheet
// leads with". These are those six, in the datasheet's own order, one from
// each thing it claims vnprox does — See, See, Understand, Understand,
// Change, Operate:
//
//   1. Topology map            docs/datasheet.md § Capabilities → See
//   2. Physical discovery      § See
//   3. Findings stream         § Understand
//   4. Flow explorer           § Understand
//   5. SDN cockpit             § Change
//   6. History & time machine  § Operate
//
// TWO OF THE DATASHEET'S LEAD ITEMS ARE DELIBERATELY ABSENT, and pretending
// otherwise would be the dishonest version of this file: the **path
// simulator** and the **diagnosis ladder** are POST-shaped read surfaces
// (POST /simulate/path, POST /diagnose), and a public demo refuses every
// mutating METHOD at the edge — there is no semantic allowlist, on purpose
// (internal/publicdemo/doc.go), a decision re-confirmed rather than
// revisited when T-3303 stood up the hosted instance. Both screens
// therefore still answer 403 there, so the tour routes around them rather
// than walking a visitor into an error. (Plain, non-public `vnproxd --demo`
// no longer has this gap — T-2801-followup-01's app-level half is resolved,
// internal/api/demo.go's demoReadOnlyPosts — but this script drives the
// public tour specifically, so it still skips both. See
// docs/features/demo-mode.md's gap list for the full state.)
//
// The prose names entities that exist in internal/demo/dataset/cluster.yaml
// — pve1/pve2/pve3, vmbr0, vnet100/vnet200, app01/cache01/db01 — because a
// tour that says "look at your bridges" on a fixture nobody can change is a
// tour that could have been written without looking at the data. Renaming
// any of them breaks tourScript.test.ts, which is the intended coupling.

/** One tour stop. Deliberately data, not components: a script is reviewable
 * as prose, and the panel that renders it (GuidedTour.tsx) has no per-step
 * branches at all. */
export interface TourStep {
  /** Stable id, persisted in the visitor's own progress. Renaming one
   * invalidates saved progress, which is why they are short and generic. */
  readonly id: string;
  /** Where this stop lives. Every route here is served by GET requests
   * only — see the note above about the two that are not. */
  readonly route: string;
  readonly title: string;
  readonly body: string;
  /** What the visitor should be able to see, named concretely enough to be
   * checkable. Rendered as the step's "look for" line. */
  readonly lookFor: string;
}

export const TOUR_STEPS: readonly TourStep[] = [
  {
    id: "map",
    route: "/topology",
    title: "The map",
    body:
      "This is a three-node Proxmox cluster drawn from what vnprox read: nodes, NICs, bonds, bridges, SDN VNets and guests, " +
      "in one picture. Nothing here was configured in vnprox — it read pve1, pve2 and pve3 and drew what it found.",
    lookFor: "pve1, pve2 and pve3, with vmbr0 on each and the guests app01, cache01 and db01 hanging off them.",
  },
  {
    id: "physical",
    route: "/ports",
    title: "Every port, physical and virtual",
    body:
      "The same cluster as a table: which NIC is in which bond, what each port carries, and — where lldpd is running — " +
      "the switch name and port on the other end of the cable.",
    lookFor: "the per-node port list, with each node's uplinks and their link state.",
  },
  {
    id: "findings",
    route: "/tools",
    title: "What vnprox noticed",
    body:
      "Findings are the things vnprox thinks are wrong: MTU mismatches, half-applied configuration, drift between what a " +
      "node runs and what it has staged. This demo cluster has several on purpose — a cluster with nothing wrong " +
      "demonstrates nothing.",
    lookFor: "the Findings list, and the drift entries for the staged interfaces file and the diverged bridge MTU.",
  },
  {
    id: "flows",
    route: "/flows",
    title: "What is actually talking",
    body:
      "Observed traffic, attributed to a service class — migration, backup, Ceph, corosync — from metadata alone, never " +
      "payload inspection. This is where 'which guest is saturating the uplink' stops being a guess.",
    lookFor: "flow rows between the demo guests, with a service class on each.",
  },
  {
    id: "sdn",
    route: "/sdn",
    title: "SDN, with realization state",
    body:
      "Zones, VNets and subnets, each with per-node realization status — staged versus running shown as a real diff " +
      "rather than an opaque 'pending' flag. On a real cluster this is also where you would create one.",
    lookFor: "vnet100 and vnet200, and their per-node status.",
  },
  {
    id: "history",
    route: "/history",
    title: "What changed, and when",
    body:
      "Every change vnprox made, and every change it did not — drift is what someone did by hand. On a real cluster you " +
      "can diff any two points and restore either through the normal staged review. Here, everything is read-only: this " +
      "is a public demo, and every write is refused before it reaches the daemon.",
    lookFor: "the timeline of events this demo cluster has accumulated since it started.",
  },
];

/** The step ids, in order. Exported for the machine and for tests. */
export const TOUR_STEP_IDS: readonly string[] = TOUR_STEPS.map((s) => s.id);

/** The step with this id, or undefined. */
export function tourStep(id: string): TourStep | undefined {
  return TOUR_STEPS.find((s) => s.id === id);
}
