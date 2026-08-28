// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/ha"
)

type fakeHAProvider struct{ st ha.Status }

func (p fakeHAProvider) Status() ha.Status { return p.st }

func TestCheckHAReplicationDegraded(t *testing.T) {
	if got := checkHAReplicationDegraded(nil); got != nil {
		t.Errorf("nil provider -> %v, want nil", got)
	}

	healthy := fakeHAProvider{st: ha.Status{Role: "active", ReplicationDegraded: false, ReplicationLag: 3}}
	if got := checkHAReplicationDegraded(healthy); got != nil {
		t.Errorf("healthy provider -> %v, want nil (no finding)", got)
	}

	degraded := fakeHAProvider{st: ha.Status{Role: "active", ReplicationDegraded: true, ReplicationLag: 900, LastError: "circuit open"}}
	got := checkHAReplicationDegraded(degraded)
	if len(got) != 1 {
		t.Fatalf("degraded provider -> %d findings, want 1", len(got))
	}
	if got[0].Check != CheckHAReplicationDegraded || got[0].Severity != SeverityWarning {
		t.Errorf("finding = %+v, want %s / warning", got[0], CheckHAReplicationDegraded)
	}
}
