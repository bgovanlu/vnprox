package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
)

// maxStdioMessageBytes bounds a single newline-delimited JSON-RPC message on
// the stdio transport — generous headroom for a changeset with many ops, a
// guard against an abusive/runaway client rather than a realistic limit.
const maxStdioMessageBytes = 8 << 20 // 8 MiB

// ErrSessionRevoked is returned by ServeStdio when the serve loop ended because
// the authenticating token was revoked mid-session (AC5), as opposed to a clean
// EOF/transport close.
var ErrSessionRevoked = errors.New("mcp: session closed — token revoked")

// ServeStdio runs the MCP server over a newline-delimited JSON-RPC stream
// (MCP's stdio transport): it reads one JSON-RPC message per line from r,
// dispatches it for session, and writes each response as one line to w. It
// returns when parent is cancelled, r reaches EOF, a write fails, or — the
// security-relevant case — the token is revoked, in which case it returns
// ErrSessionRevoked within one revocation tick.
//
// session must already be authenticated (Server.Authenticate). The reader loop
// runs in its own goroutine so a revocation can unblock a serve that is parked
// waiting for the next client line.
func (s *Server) ServeStdio(parent context.Context, session *Session, r io.Reader, w io.Writer) error {
	ctx, cancel := session.watch(parent)
	defer cancel()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), maxStdioMessageBytes)
		for scanner.Scan() {
			// Copy: Scanner reuses its buffer across Scan calls.
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		readErr <- scanner.Err()
	}()

	writer := bufio.NewWriter(w)
	for {
		select {
		case <-ctx.Done():
			// Distinguish a revocation-driven close from a parent cancel.
			if parent.Err() == nil {
				return ErrSessionRevoked
			}
			return ctx.Err()
		case err := <-readErr:
			return err // nil on clean EOF
		case line := <-lines:
			if len(line) == 0 {
				continue
			}
			resp := s.HandleMessage(ctx, session, line)
			if resp == nil {
				continue // notification: no reply
			}
			if _, err := writer.Write(resp); err != nil {
				return err
			}
			if err := writer.WriteByte('\n'); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
		}
	}
}
