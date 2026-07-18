package capture

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// newID mirrors store.NewULID (lexicographically sortable, monotonic) —
// duplicated rather than imported so internal/capture never depends on
// internal/store (the same import-direction discipline internal/flow keeps).
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

func newID() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}
