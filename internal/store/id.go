package store

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// entropy is a monotonic ULID entropy source. ulid.Monotonic is not safe for
// concurrent use on its own, so every call to NewULID takes entropyMu.
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewULID returns a new lexicographically-sortable ULID string, used for
// changesets.id per docs/data-model.md §2's "ULID" comment. It is safe for
// concurrent use.
func NewULID() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}
