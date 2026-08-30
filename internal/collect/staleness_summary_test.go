// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// The chain an operator was actually shown, reproduced by WRAPPING the same
// sentinels the real path wraps rather than by pasting its text. A test built
// on the joined string would pass while errors.Is stopped matching, which is
// the whole failure mode this summariser is designed around.
func untrustedPeerChain() error {
	caErr := fmt.Errorf("reading /etc/pve/pve-root-ca.pem: %w",
		&os.PathError{Op: "open", Path: "/etc/pve/pve-root-ca.pem", Err: os.ErrNotExist})
	anchor := fmt.Errorf("peer: %w: %w", peer.ErrTrustAnchorUnavailable, caErr)
	// peer/errors.go: an untrusted peer wraps unreachable too, deliberately.
	untrusted := fmt.Errorf("peer: pve2: %w (%w): %w", peer.ErrPeerUntrusted, peer.ErrPeerUnreachable, anchor)
	return fmt.Errorf("host links (pve2): %w", untrusted)
}

func TestSummarizeSourceError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		node     string
		contains []string
		notWant  []string
	}{
		{
			name: "trust anchor missing names the file and the likely cause",
			err:  untrustedPeerChain(),
			node: "pve2",
			// The local anchor failure must win over both peer sentinels it is
			// wrapped with: every peer will be reporting untrusted, and telling
			// the operator to run pvecm updatecerts on each of them would be
			// wrong advice confidently given.
			contains: []string{"pve2", "no cluster CA", "/etc/pve/pve-root-ca.pem", "pmxcfs"},
			notWant:  []string{"pvecm updatecerts"},
		},
		{
			name:     "untrusted peer without a local anchor problem gives the fix command",
			err:      fmt.Errorf("host links (pve3): peer: pve3: %w (%w)", peer.ErrPeerUntrusted, peer.ErrPeerUnreachable),
			node:     "pve3",
			contains: []string{"pve3", "certificate", "pvecm updatecerts -f"},
			notWant:  []string{"did not answer"},
		},
		{
			name:     "plain unreachable is not reported as a trust failure",
			err:      fmt.Errorf("host links (pve4): %w", peer.ErrPeerUnreachable),
			node:     "pve4",
			contains: []string{"pve4", "did not answer", "last known good"},
			notWant:  []string{"certificate", "pvecm"},
		},
		{
			name:     "incompatible peer says upgrade, not restart",
			err:      fmt.Errorf("version: %w", peer.ErrPeerIncompatible),
			node:     "pve5",
			contains: []string{"pve5", "incompatible", "upgrade"},
		},
		{
			name:     "missing cluster secret is a local fault, so it does not name a peer",
			err:      fmt.Errorf("sign: %w", peer.ErrNoSecret),
			node:     "pve2",
			contains: []string{"cluster secret"},
			notWant:  []string{"pve2"},
		},
		{
			name: "context cancellation reads as a restart, not a fault",
			// T-3603's header quotes the banner this replaces verbatim:
			// "no successful poll yet — context canceled".
			err:      fmt.Errorf("poll: %w", context.Canceled),
			node:     "",
			contains: []string{"stopped", "normal during a restart"},
		},
		{
			name:     "timeout is scoped to the node that timed out",
			err:      fmt.Errorf("poll: %w", context.DeadlineExceeded),
			node:     "pve2",
			contains: []string{"pve2", "timed out"},
		},
		{
			name:     "a peer that answered with an error says so, with its status",
			err:      fmt.Errorf("host links: %w", &peer.ResponseError{Code: "node_unknown", StatusCode: 404}),
			node:     "pve2",
			contains: []string{"pve2", "404", "node_unknown"},
			notWant:  []string{"did not answer"},
		},
		{
			name: "cluster-wide source has no node, so it says 'this node'",
			err:  fmt.Errorf("pve: %w", peer.ErrPeerUnreachable),
			node: "",
			// Not "" — a cluster-wide source still needs a grammatical subject.
			contains: []string{"this node"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := summarizeSourceError(tc.err, tc.node)
			if got == "" {
				t.Fatalf("expected a summary, got none")
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("summary missing %q\ngot: %s", want, got)
				}
			}
			for _, unwanted := range tc.notWant {
				if strings.Contains(got, unwanted) {
					t.Errorf("summary should not contain %q\ngot: %s", unwanted, got)
				}
			}
		})
	}
}

func TestSummarizeSourceErrorStaysSilentRatherThanGuessing(t *testing.T) {
	t.Parallel()

	// An empty summary omits the JSON field, and the banner falls back to the
	// raw chain. That is the honest outcome for an error this function does
	// not recognise: a confident paraphrase of an unknown failure is worse
	// than the unreadable truth, because the operator cannot tell it is wrong.
	for _, err := range []error{
		nil,
		errors.New("something nobody has classified yet"),
		fmt.Errorf("wrapped: %w", errors.New("still unclassified")),
	} {
		if got := summarizeSourceError(err, "pve2"); got != "" {
			t.Errorf("expected no summary for %v, got: %s", err, got)
		}
	}
}

func TestSummaryNeverReplacesTheChain(t *testing.T) {
	t.Parallel()

	// The contract decision on T-4304 was Option B: the summary is a SIBLING.
	// This asserts the property that made B worth the extra field — the full
	// wrap is still there for a bug report — at the layer that builds both.
	chain := untrustedPeerChain()
	st := &sourceState{lastErr: chain}
	got := toSourceStatus("host", "pve2", st)

	if got.LastError != chain.Error() {
		t.Errorf("LastError must carry the chain byte for byte\n got: %s\nwant: %s", got.LastError, chain.Error())
	}
	if got.LastErrorSummary == "" {
		t.Fatal("expected a summary alongside the chain")
	}
	if got.LastErrorSummary == got.LastError {
		t.Error("summary and chain are identical — the summary is not summarising")
	}
	if len(got.LastErrorSummary) >= len(got.LastError) {
		t.Errorf("summary (%d chars) is not shorter than the chain (%d chars)",
			len(got.LastErrorSummary), len(got.LastError))
	}
}
