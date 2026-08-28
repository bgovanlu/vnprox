// SPDX-License-Identifier: Apache-2.0

package vitestgate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/e2egate"
)

// TestRepoQuarantineIsValid is the check that keeps the shipped file honest,
// mirroring internal/e2egate's TestRepoQuarantineIsValid for
// web/e2e/quarantine.json. Every rule in internal/e2egate.Validate applies
// to cmd/vitestgate/quarantine.json itself, evaluated against the real
// clock, so an entry whose expiry has quietly passed turns `make check` red
// without anyone re-running vitest.
func TestRepoQuarantineIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "vitestgate", "quarantine.json")
	q, err := e2egate.LoadQuarantine(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	for _, p := range e2egate.Validate(q, time.Now()) {
		t.Errorf("%s: %s %s — %s", path, p.Entry.File, p.Entry.Title, p.Reason)
	}
	for _, e := range q.Entries {
		expired, expErr := e.Expired(time.Now())
		if expErr != nil {
			t.Errorf("%s: %v", path, expErr)
			continue
		}
		if expired {
			t.Errorf("%s: quarantine for %s %s expired on %s (%s) — fix the test or re-triage the entry",
				path, e.File, e.Title, e.Expires, e.Ticket)
		}
	}
}
