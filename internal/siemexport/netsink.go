// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// dialTimeout bounds netSink's dial-on-demand attempt — long enough for a
// real network round trip, short enough not to stall Exporter.Run's
// per-attempt sendTimeout budget.
const dialTimeout = 3 * time.Second

// netSink is the shared "dial on demand, redial after any write error"
// transport both sink_syslog.go (tcp/udp) and sink_jsonl.go's
// network-socket mode (tcp/udp/unix) use. It holds at most one connection
// at a time and never retries a write itself — Exporter.Run's own
// at-most-once/backoff loop (doc.go's "Delivery semantics") is what paces
// retries; this type's only job is "reconnect lazily, never hold a
// connection known to be broken".
//
// allocation path.
//
//nolint:govet // fieldalignment: one netSink per Sink, never a hot
type netSink struct {
	network string // "tcp" | "udp" | "unix"
	address string
	conn    net.Conn
	mu      sync.Mutex
}

func newNetSink(network, address string) *netSink {
	return &netSink{network: network, address: address}
}

// write dials (if not already connected) and writes frame as a single
// Write call. On any error the held connection is closed and discarded,
// so the NEXT write attempt (this event's retry-as-a-fresh-attempt is
// Exporter's concern, not this type's — see doc.go) dials fresh: this is
// the entire reconnect mechanism, exercised by exporter_test.go's
// reconnect case by starting a listener only after the first Send has
// already failed.
func (n *netSink) write(ctx context.Context, frame []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.conn == nil {
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(ctx, n.network, n.address)
		if err != nil {
			return fmt.Errorf("siemexport: dialing %s %s: %w", n.network, n.address, err)
		}
		n.conn = conn
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = n.conn.SetWriteDeadline(dl)
	}
	if _, err := n.conn.Write(frame); err != nil {
		_ = n.conn.Close()
		n.conn = nil
		return fmt.Errorf("siemexport: writing to %s %s: %w", n.network, n.address, err)
	}
	return nil
}

// close releases the held connection, if any.
func (n *netSink) close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn == nil {
		return nil
	}
	err := n.conn.Close()
	n.conn = nil
	return err
}
