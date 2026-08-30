// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// T-4304 deliverable 3: an operator-facing sentence for the staleness banner.
//
// What the banner shows today is the poll error verbatim, and a poll error is
// a five-level wrap by the time it surfaces:
//
//	host links (pve2): peer: pve2: peer_untrusted (peer_unreachable): circuit
//	open after a certificate verification failure: peer: pve2: peer_untrusted:
//	certificate verification failed, treating the peer as unreachable
//	(peer_unreachable): Get "https://10.20.0.12:8007/api/peer/host/links?node=pve2":
//	peer: peer_untrusted: cluster CA trust anchor unavailable: reading
//	/etc/pve/pve-root-ca.pem: open /etc/pve/pve-root-ca.pem: no such file or
//	directory
//
// Every clause is true and the whole is unreadable. The operator needs one
// fact — this peer's certificate cannot be verified because the cluster CA
// file is missing — and one command.
//
// **Derived from the sentinels, not from the text.** Every one of those
// clauses is wrapped with %w, so errors.Is answers the question directly.
// Parsing the joined string would be a fourth copy of knowledge the error
// values already carry, and would break the first time a wrap message was
// reworded — the same defect class T-4301 found in a hand-copied palette.
//
// **The chain is not replaced.** SourceStatus.LastError keeps it byte for
// byte; this is a sibling field (docs/api.md's `lastErrorSummary`). Option A
// on T-4304's card — reuse the documented `lastError` — was rejected because
// it silently redefines a field other consumers read and throws away the
// chain that is genuinely useful in a bug report. Both audiences are real.
//
// Order matters below: an untrusted peer deliberately wraps
// ErrPeerUnreachable too (see peer/errors.go — "an unverifiable peer is
// unreachable, never trusted"), so the more specific cause has to be tested
// first or every trust failure reports as a plain outage.

// summarizeSourceError renders one poll error as a single operator-facing
// sentence: what is wrong, and where possible what to do about it. node is
// the cluster member the source is scoped to ("" for cluster-wide sources);
// it is used only to name the subject, never to decide the cause.
//
// Returns "" when the error is nil or nothing more useful than the raw chain
// can be said. An empty summary omits the JSON field entirely, which is the
// honest signal: the banner then falls back to the chain rather than showing
// a confidently wrong paraphrase of an error this function does not know.
func summarizeSourceError(err error, node string) string {
	if err == nil {
		return ""
	}

	subject := "this node"
	if node != "" {
		subject = node
	}

	switch {
	// Trust first — see the ordering note above.
	case errors.Is(err, peer.ErrTrustAnchorUnavailable):
		// The local cause, not the peer's fault: this daemon has no anchor to
		// verify anyone against, so EVERY peer will be reporting untrusted.
		// Naming the file is the whole value here — it is almost always an
		// unmounted /etc/pve.
		return fmt.Sprintf(
			"%s could not be verified because this node has no cluster CA to check it against%s. "+
				"That usually means /etc/pve is not mounted, so pmxcfs is not running.",
			subject, trustAnchorPath(err))
	case errors.Is(err, peer.ErrPeerUntrusted):
		return fmt.Sprintf(
			"%s answered, but its TLS certificate did not verify against the cluster CA, so it is being "+
				"treated as unreachable. To fix: on %s, run  pvecm updatecerts -f",
			subject, subject)
	case errors.Is(err, peer.ErrPeerIncompatible):
		return fmt.Sprintf(
			"%s is running an incompatible vnprox protocol version, so cluster-wide changes involving it "+
				"are refused. To fix: upgrade both nodes to the same vnprox release.",
			subject)
	case errors.Is(err, peer.ErrNoSecret):
		return "This node has no cluster secret loaded, so it can neither sign nor verify peer requests. " +
			"To fix: check that the vnprox cluster secret file exists and is readable."
	case errors.Is(err, peer.ErrPeerUnreachable):
		return fmt.Sprintf(
			"%s did not answer on its vnprox peer port. Its data is the last known good. "+
				"To fix: check that vnprox is running on %s and reachable on port 8007.",
			subject, subject)

	// Not a peer problem at all.
	case errors.Is(err, context.Canceled):
		// T-3603's header opens by quoting this exact banner — "no successful
		// poll yet — context canceled" — which said nothing an operator could
		// act on. It is a shutdown, not a fault.
		return "The collector was stopped before this poll finished. This is normal during a restart; " +
			"the banner clears on the next successful poll."
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("The poll of %s timed out. Its data is the last known good.", subject)
	case errors.Is(err, os.ErrPermission):
		return "This poll was refused by the operating system. vnproxd usually needs to run as root to " +
			"read network state."
	}

	// A ResponseError means the peer is alive and said no, which is a
	// different operational fact from silence and is worth saying plainly —
	// its own Code/Message came from the peer's error envelope.
	var respErr *peer.ResponseError
	if errors.As(err, &respErr) {
		return fmt.Sprintf(
			"%s answered the poll with an error (HTTP %d%s). Its data is the last known good.",
			subject, respErr.StatusCode, formatPeerCode(respErr.Code))
	}

	return ""
}

// trustAnchorPath extracts the CA path the trust error names, so the sentence
// can point at a file rather than at a concept. Best-effort by design: it
// reads the path out of the wrapped *os.PathError if one is present and says
// nothing at all otherwise, because a summary that guesses a path an operator
// then cannot find is worse than one that omits it.
func trustAnchorPath(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Path != "" {
		return " (" + pathErr.Path + " is missing)"
	}
	return ""
}

func formatPeerCode(code string) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	return ", " + code
}
