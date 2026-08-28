// SPDX-License-Identifier: Apache-2.0

// QoS shaping (docs/api.md's QoS section, T-1505; internal/api/qos.go).
//
// GET /qos/shapes is the *only* QoS route this daemon serves. Creating,
// editing and deleting a shape is a `qos.shape.create|update|delete`
// changeset op (internal/change/op.go), staged and applied through the
// change engine like everything else — so this module deliberately exports
// no mutation function at all. If you find yourself wanting one, the op
// builders in ../analysis/qosOps.ts are what you actually want.
import { apiFetch } from "./client";
import type { QosShape, QosShapesView } from "./types";

/** GET /qos/shapes — every currently-stored shape. A read view onto the
 * app-owned `qos_shapes` table, never a live `tc` read, so a shape listed
 * here is one vnprox has applied, not one it has just observed. */
export function fetchQosShapes(): Promise<QosShape[]> {
  return apiFetch<QosShapesView>("/qos/shapes").then((r) => r.shapes);
}
