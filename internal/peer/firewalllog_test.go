// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
)

// spyFirewallLogReader is an in-memory FirewallLogReader test double
// (T-505): per-node line buffers with an offset-based cursor, mirroring
// internal/fwlog's own MemorySource semantics closely enough for these
// transport-level tests (this package must not import internal/fwlog —
// see FirewallLogReader's doc comment).
type spyFirewallLogReader struct {
	lines map[string][]string
	calls int
}

func newSpyFirewallLogReader() *spyFirewallLogReader {
	return &spyFirewallLogReader{lines: map[string][]string{}}
}

func (s *spyFirewallLogReader) FirewallLogTail(_ context.Context, node, cursor string, maxLines int) ([]string, string, error) {
	s.calls++
	all, ok := s.lines[node]
	if !ok {
		return nil, "", errors.Join(host.ErrNotFound, errors.New("node "+node))
	}
	start := 0
	if cursor != "" {
		for i, c := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10"} {
			if c == cursor {
				start = i
				break
			}
		}
	}
	end := start + maxLines
	if end > len(all) {
		end = len(all)
	}
	if start > len(all) {
		start = len(all)
	}
	out := all[start:end]
	next := start + len(out)
	nextCursor := ""
	if next > 0 {
		nextCursor = []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}[next]
	}
	return out, nextCursor, nil
}

// TestHandleFirewallLog_TailAndFollow is T-505's peer-transport
// counterpart to TestTwoDaemonHarness_Links/FDB: GET /api/peer/firewall/log
// serves a node's lines and advances the cursor correctly across two
// calls, round-tripping through Client.FirewallLog.
func TestHandleFirewallLog_TailAndFollow(t *testing.T) {
	reader := newSpyFirewallLogReader()
	reader.lines["pve1"] = []string{"line-a", "line-b", "line-c"}

	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		FirewallLog: reader,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	lines, cursor, err := client.FirewallLog(context.Background(), p, "pve1", "", 2)
	if err != nil {
		t.Fatalf("FirewallLog: %v", err)
	}
	if len(lines) != 2 || lines[0] != "line-a" || lines[1] != "line-b" {
		t.Fatalf("lines = %v, want [line-a line-b]", lines)
	}

	more, _, err := client.FirewallLog(context.Background(), p, "pve1", cursor, 10)
	if err != nil {
		t.Fatalf("FirewallLog (follow): %v", err)
	}
	if len(more) != 1 || more[0] != "line-c" {
		t.Fatalf("follow lines = %v, want [line-c]", more)
	}
	if reader.calls != 2 {
		t.Errorf("reader.calls = %d, want 2", reader.calls)
	}
}

// TestHandleFirewallLog_UnknownNode covers the reader's ErrNotFound
// surfacing as a peer ResponseError, matching Links/FDB's own convention.
func TestHandleFirewallLog_UnknownNode(t *testing.T) {
	reader := newSpyFirewallLogReader()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		FirewallLog: reader,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	if _, _, err := client.FirewallLog(context.Background(), p, "no-such-node", "", 10); err == nil {
		t.Fatal("expected an error for an unknown node")
	}
}

// TestHandleFirewallLog_Unconfigured503s mirrors LLDPInstaller's own
// nil-safety test: a daemon that hasn't wired a firewall log source
// refuses the route with 503, never a panic.
func TestHandleFirewallLog_Unconfigured503s(t *testing.T) {
	srv := NewServer(ServerOptions{Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger()})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	if _, _, err := client.FirewallLog(context.Background(), p, "pve1", "", 10); err == nil {
		t.Fatal("expected an error when FirewallLog is not configured")
	}
}

// TestHandleFirewallLog_MaxLinesValidation covers ?maxLines= input
// validation, mirroring parsePeerPageLimit's own tested convention.
func TestHandleFirewallLog_MaxLinesValidation(t *testing.T) {
	reader := newSpyFirewallLogReader()
	reader.lines["pve1"] = []string{"a"}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		FirewallLog: reader,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	// maxLines=0 is treated as "unset" by Client.FirewallLog (it only sets
	// the query param when > 0), so this exercises the server's own default
	// rather than a validation error.
	lines, _, err := client.FirewallLog(context.Background(), p, "pve1", "", 0)
	if err != nil {
		t.Fatalf("FirewallLog(maxLines=0): %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want 1 (server default maxLines)", lines)
	}
}
