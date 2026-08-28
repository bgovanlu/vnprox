// SPDX-License-Identifier: Apache-2.0

package k8s

import "errors"

// Sentinel errors, following docs/development.md's "sentinel errors in
// each package's errors.go" convention.
var (
	// ErrNoCurrentContext means the kubeconfig names no current-context (or
	// names one absent from its own contexts list) — there is nothing to
	// connect to.
	ErrNoCurrentContext = errors.New("k8s: kubeconfig has no resolvable current-context")
	// ErrUnknownCluster means a context's cluster name is absent from the
	// kubeconfig's clusters list.
	ErrUnknownCluster = errors.New("k8s: context names an unknown cluster")
	// ErrUnknownUser means a context's user name is absent from the
	// kubeconfig's users list.
	ErrUnknownUser = errors.New("k8s: context names an unknown user")
	// ErrNoServer means the resolved cluster entry carries no server URL.
	ErrNoServer = errors.New("k8s: cluster entry has no server URL")
	// ErrNoCredential means the resolved user entry carries none of the
	// credential forms this package understands (bearer token, client
	// cert+key) — never a write credential, only ever used to authenticate
	// the read-only requests client.go issues.
	ErrNoCredential = errors.New("k8s: user entry has no usable credential (token or client cert+key)")
)
