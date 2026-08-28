// SPDX-License-Identifier: Apache-2.0

// Package k8smock is T-1501's hardware-free k8s API server double: an
// httptest.Server serving the exact four read-only endpoints
// internal/k8s.Client calls (/api/v1/nodes, /api/v1/pods,
// /api/v1/services, /apis/apps/v1/namespaces/kube-system/daemonsets),
// driven by YAML fixture files under testdata/k8s/ — mirroring
// internal/pvemock's own httptest-server-driven-by-fixture convention, and
// internal/ingress/ingressmock's "exported, not test-only, so other
// packages' tests can reuse it verbatim" precedent (internal/api's k8s
// route tests and internal/findings' k8s-finding adapter tests both build
// on this package rather than each hand-rolling their own server).
//
// Every server this package builds also records every request it
// received (Recorder), so a caller can assert on the exact HTTP methods
// a Client issued — the fixture half of T-1501 AC4's zero-write-surface
// regression test (internal/k8s/zerowrite_test.go).
package k8smock
