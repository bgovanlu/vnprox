// Package spec implements T-1101's declarative cluster network spec:
// blueprints v2, cluster-scoped. One versionable YAML document (Spec)
// captures cluster-wide L2/SDN network intent — per-node bonds, bridges and
// VLAN sub-interfaces, plus cluster-scoped SDN zones/vnets/subnets — rendered
// from live inventory state by Export and reconciled back against live state
// by Import.
//
// Two properties are load-bearing and must not regress (both are frozen by
// this review-checkpoint task; the rest of Phase 11 builds on them):
//
//  1. Byte-stable serialization. Spec has NO embedded timestamp and is a
//     tree of typed structs with fixed struct-tag field order — never a
//     map[string]any (Go map iteration is randomized). Marshal therefore
//     emits byte-identical YAML for two exports of identical live state,
//     which is what makes a GitOps `git diff` on the committed spec show an
//     empty diff for an unchanged cluster (docs/data-model.md §5).
//
//  2. Import never applies. Import diffs the parsed spec against live and
//     returns an ordered []change.Op plus a notInSpec list; it does not
//     touch the change engine or PVE. The caller (internal/api's
//     POST /spec/import handler) hands the ops to change.Service.Create,
//     producing an ordinary DRAFT changeset that flows through the normal
//     stage → validate → diff → apply → confirm/rollback lifecycle. Entities
//     present live but absent from the spec are REPORTED in notInSpec, never
//     turned into delete ops — there is no implicit prune.
//
// The per-entity diff logic mirrors internal/blueprint's
// absent→create / divergent→update / matching→noop pattern (see that
// package's adapters.go), extended from one blueprint's node-selected set to
// every cluster-wide entity of the managed kinds. The managed kinds are
// exactly the ones the blueprint diff engine already covers — bridge, bond,
// vlan, sdn-zone, sdn-vnet, sdn-subnet — so a spec import and a blueprint
// instantiation produce the same op vocabulary. Firewall rulesets and IPAM
// allocations are deliberately out of the v1 spec's reconcile scope (see
// docs/data-model.md §5 and planning/reports/T-1101.md); a later spec version
// may add them additively.
package spec
