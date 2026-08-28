// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
)

// TestDetectCNI_FixtureVariants is T-1501 AC1: DetectCNI against each of
// the three named fixture variants returns the correct value, and a
// fourth fixture with no recognizable CNI marker returns CNIUnknown.
func TestDetectCNI_FixtureVariants(t *testing.T) {
	tests := []struct {
		fixture string
		want    k8s.CNI
	}{
		{"cluster-flannel.yaml", k8s.CNIFlannel},
		{"cluster-calico.yaml", k8s.CNICalico},
		{"cluster-cilium.yaml", k8s.CNICilium},
		{"cluster-unknown.yaml", k8s.CNIUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			f, err := k8smock.LoadFixtureFile("../../testdata/k8s/" + tc.fixture)
			if err != nil {
				t.Fatalf("LoadFixtureFile: %v", err)
			}
			nodes, _, _, daemonsets := f.ToK8s()
			got := k8s.DetectCNI(nodes, daemonsets)
			if got != tc.want {
				t.Errorf("DetectCNI(%s) = %q, want %q", tc.fixture, got, tc.want)
			}
		})
	}
}

func TestDetectCNI_EmptyInputIsUnknown(t *testing.T) {
	if got := k8s.DetectCNI(nil, nil); got != k8s.CNIUnknown {
		t.Errorf("DetectCNI(nil, nil) = %q, want CNIUnknown", got)
	}
}

func TestDetectCNI_DaemonSetMarkerWinsOverAbsentAnnotation(t *testing.T) {
	nodes := []k8s.Node{{Metadata: k8s.ObjectMeta{Name: "n1"}}}
	ds := []k8s.DaemonSet{{Metadata: k8s.ObjectMeta{Name: "cilium", Namespace: "kube-system"}}}
	if got := k8s.DetectCNI(nodes, ds); got != k8s.CNICilium {
		t.Errorf("DetectCNI = %q, want CNICilium", got)
	}
}
