// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"testing"
	"time"
)

// unreachableClient returns an *http.Client with a short timeout, used by
// every vendor's "unreachable target" test so it fails fast against a
// connection nothing is listening on rather than the package's own
// (5s) default.
func unreachableClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Timeout: 200 * time.Millisecond}
}
