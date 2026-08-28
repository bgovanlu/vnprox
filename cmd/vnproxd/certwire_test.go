// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/certs"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestCertClusterFactsFor_NilClientDegradesGracefully is the regression test
// for the daemon panic found on pve001's first cold start (before
// vnprox-setup had ever run): setupCollect returns a nil *pve.Client when no
// PVE token is configured yet, and server.go used to hand that nil pointer
// straight to certClusterFacts, whose own `src == nil` check never fires for
// it (a nil concrete pointer boxed into an interface is not itself a nil
// interface). certs.NewService then called ClusterStatus on a nil
// *pve.Client during its first synchronous Refresh(), and internal/pve's
// unguarded field access on a nil receiver turned that into a nil-pointer
// panic that crashed the whole daemon.
//
// certClusterFactsFor is the fix: it nil-checks the concrete *pve.Client
// before ever constructing the clusterStatusSource interface value, so
// certs.NewService gets a nil ClusterFactsFunc and degrades the same way the
// collector already does for this exact "no PVE client yet" condition
// (setupCollect's own doc comment).
func TestCertClusterFactsFor_NilClientDegradesGracefully(t *testing.T) {
	got := certClusterFactsFor(nil)
	if got != nil {
		t.Fatalf("certClusterFactsFor(nil) = %v, want nil ClusterFactsFunc", got)
	}

	// The real regression: build the service exactly as server.go does, with
	// a nil PVE client, and prove NewService's synchronous first Refresh()
	// does not panic — mirroring the collector's own graceful "no live PVE
	// client yet" degradation instead of crashing the daemon.
	svc := certs.NewService(certs.ServiceOptions{
		Logger: quietLogger(),
		Facts:  certClusterFactsFor(nil),
		Root:   t.TempDir(),
	})
	if svc == nil {
		t.Fatal("certs.NewService returned nil")
	}
	svc.Preflight() // must not panic either
}

// TestCertClusterFactsFor_TypedNilPVEClientGotcha documents, directly, the
// Go interface pitfall this bug turned on: passing a nil *pve.Client through
// a bare interface-typed parameter (certClusterFacts's clusterStatusSource)
// produces a non-nil interface, so the callee's own nil check silently does
// not fire. This is why the fix lives at the concrete-pointer nil check in
// certClusterFactsFor rather than by "fixing" certClusterFacts's existing
// check, which cannot see the difference.
func TestCertClusterFactsFor_TypedNilPVEClientGotcha(t *testing.T) {
	var nilClient *pve.Client // concrete nil

	// Boxing the concrete nil pointer into the narrow interface directly
	// (what certClusterFacts's parameter type is) produces a non-nil
	// interface value.
	var src clusterStatusSource = nilClient

	// No runtime assertion that src != nil here: both govet's nilness pass and
	// staticcheck (SA4023) already statically prove it — which is independent
	// confirmation of the pitfall this test documents, not something worth
	// re-asserting at runtime. certClusterFacts's own guard therefore does not catch it...
	f := certClusterFacts(src)
	if f == nil {
		t.Fatal("certClusterFacts(src) returned nil for a boxed-nil interface; the typed-nil gotcha this test documents no longer reproduces, so certClusterFactsFor's defensive nil check may no longer be load-bearing here")
	}
	// ...calling the resulting func would panic three frames down in
	// internal/pve.(*Client).do on a nil receiver; we don't invoke f here
	// (a crashing test proves nothing a t.Fatal doesn't), it's enough that a
	// non-nil ClusterFactsFunc came out of a nil client to show why the
	// caller-side concrete-pointer check in certClusterFactsFor is
	// necessary and not just belt-and-suspenders.
	_ = f
}
