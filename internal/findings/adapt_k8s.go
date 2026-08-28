// SPDX-License-Identifier: Apache-2.0

package findings

// K8sProvider is the seam internal/k8s's NodePort-exposure producer
// satisfies (via cmd/vnproxd's own adapter, which converts
// k8s.NodePortFinding values into the unified Finding shape — the
// composition root does the conversion, the identical "producer returns
// the unified shape directly, so this package never imports the domain
// package" pattern IPAMProvider (adapt_ipam.go) already established). Nil
// Config.K8s means "contribute zero k8s findings" (no cluster registered,
// or no poll has happened yet), the same nil-dependency degraded mode
// every other optional producer seam in this package uses.
type K8sProvider interface {
	Findings() []Finding
}

// k8sFindings returns p's current findings, or nil when p is nil.
func k8sFindings(p K8sProvider) []Finding {
	if p == nil {
		return nil
	}
	return p.Findings()
}
