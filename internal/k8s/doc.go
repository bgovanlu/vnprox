// Package k8s implements T-1501's Kubernetes overlay mapping engine:
// a deliberately minimal, hand-rolled kubeconfig parser + net/http REST
// client that correlates k8s cluster nodes to PVE guests, models pod/
// service network reachability, best-effort detects the cluster's CNI, and
// cross-checks NodePort/LoadBalancer service exposure against PVE's own
// firewall rules — the data model T-1502's map layer renders and a future
// T-1504-style classifier names k8s services with in the flow explorer.
//
// # Kubernetes integration is READ-ONLY FOREVER
//
// This is not a k8s management tool and never will be — carried forward
// from docs/roadmap-universal.md's Phase 15 Invariants section and restated
// here as this package's own binding contract:
//
//   - Client (client.go) issues exclusively http.MethodGet requests against
//     four fixed paths (/api/v1/nodes, /api/v1/pods, /api/v1/services,
//     /apis/apps/v1/namespaces/kube-system/daemonsets) — no other method,
//     no other path, ever. zerowrite_test.go asserts this two ways: static
//     source inspection (no file in this package, excluding tests,
//     references a mutating http.Method* constant or verb literal) and
//     live behavior (every Client method driven against an instrumented
//     k8smock server, asserting every request it received was GET).
//   - No credential this package handles is ever write-scoped: a
//     kubeconfig's bearer token/client cert is used only to authenticate
//     the GET requests above. There is no code path anywhere in this
//     package, internal/api's k8s routes, or internal/store's k8s_clusters
//     table that could stage, construct, or send a mutating request to a
//     k8s API server.
//   - internal/store's k8s_clusters table (docs/data-model.md §2) holds
//     app-owned intent only — which clusters to poll, and how to
//     authenticate to them — never a shadow copy of the cluster's own live
//     state (docs/architecture.md §7's new-domain invariant, the identical
//     boundary T-1401/T-1403/T-1404/T-1406 already established for their
//     own domains). GET /k8s/{clusterId}/overlay computes the overlay
//     fresh from a live poll; nothing this package returns is ever
//     persisted as authoritative.
//
// # Why not client-go
//
// **Flagged new-dependency decision (per CLAUDE.md):** client-go's
// transitive dependency graph (apimachinery, klog, multiple auth plugin
// packages, generated clientsets far larger than the four read-only list
// calls this package needs) was rejected in favor of this hand-rolled
// reader — a minimal kubeconfig parser reusing T-1101's already-approved
// gopkg.in/yaml.v3 dependency (not a second YAML library) plus a
// net/http-only REST client. This mirrors docs/development.md's "prefer
// stdlib" rule and internal/ingress's own precedent (T-1406: hand-rolled
// vendor HTTP readers rather than pulling in each vendor's own SDK).
//
// # Pod/service network model — an honest representation, not a guess
//
// A real k8s API server exposes each node's advertised pod CIDR
// (Node.spec.podCIDR/podCIDRs) directly — a genuine CIDR block, not
// inferred. It does **not** expose a cluster's service CIDR anywhere in
// the four read-only endpoints this package calls (that value lives in
// kube-apiserver's own startup flags, not in any object this package is
// allowed to read); computing one by finding the smallest CIDR containing
// every observed Service ClusterIP would be a guess dressed up as a fact.
// Overlay therefore never claims a "service CIDR": it carries pod CIDRs
// (real, per-node) plus the exact, individually-observed Service
// ClusterIPs/Pod IPs — K8sResolver (resolver.go) matches an address
// against those exact addresses first, falling back to pod-CIDR
// containment (naming the owning node's pod subnet, never a specific pod)
// only when no exact match exists. This is the identical "prefer a
// precise match, degrade to the broadest true statement, never guess"
// discipline internal/flow.GraphResolver already documents for its own
// Bridge/SdnSubnet resolution — K8sResolver is deliberately shaped to
// compose alongside it the same way (see resolver.go's doc comment).
package k8s
