package flow

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// Default UDP ports for the three wire protocols this package listens for
// (sFlow's/NetFlow's/IPFIX's own conventional well-known ports) — vnprox.toml's
// [flows] section may override any of them per node.
const (
	DefaultSFlowPort   = 6343
	DefaultNetFlowPort = 2055 // shared convention for both NetFlow v5 and v9
	DefaultIPFIXPort   = 4739
)

// DefaultMaxDatagramSize bounds how many bytes are read per UDP receive —
// UDP's own maximum payload size (65507 bytes for IPv4; rounded up to the
// nearest convenient buffer size here since a short read is harmless, only
// a buffer too small to hold a legitimate max-size datagram would silently
// truncate one).
const DefaultMaxDatagramSize = 65535

// DecodeFunc is the shape every protocol decoder in this package (sflow.go/
// netflow5.go/netflow9.go/ipfix.go) presents to Listener once wrapped by
// its own New*Listener constructor: normalize data (received from
// exporterKey, this node's own name) into Records, dropped (records/sets
// skipped for a defensible, already-logged reason — an unknown template,
// mainly), and err (the datagram itself was malformed beyond recovery).
type DecodeFunc func(data []byte, node string, now int64, exporterKey string) (records []Record, dropped int, err error)

// Listener runs one opt-in, per-node UDP listener for one protocol (sFlow,
// NetFlow v5, NetFlow v9, or IPFIX — see the New*Listener constructors).
// Off unless Run is actually invoked by the daemon's wiring (cmd/vnproxd),
// which only happens when that protocol's own vnprox.toml [flows]
// *_enabled key is true on this node — matching T-1004's opt-in convention
// and CLAUDE.md's "everything is cluster-aware" rule (a listener enabled on
// one node says nothing about any other node's config).
type Listener struct {
	Decode          DecodeFunc
	Now             func() time.Time
	Logger          *slog.Logger
	ready           chan struct{}
	Addr            string
	Node            string
	MaxDatagramSize int
}

// NewSFlowListener builds a Listener decoding sFlow v5 datagrams.
func NewSFlowListener(addr, node string) *Listener {
	return &Listener{
		Addr: addr, Node: node,
		Decode: func(data []byte, node string, now int64, _ string) ([]Record, int, error) {
			return DecodeSFlow(data, node, now)
		},
	}
}

// NewNetFlow5Listener builds a Listener decoding NetFlow v5 datagrams
// (template-less, so no TemplateCache is needed).
func NewNetFlow5Listener(addr, node string) *Listener {
	return &Listener{
		Addr: addr, Node: node,
		Decode: func(data []byte, node string, _ int64, _ string) ([]Record, int, error) {
			records, err := DecodeNetFlow5(data, node)
			return records, 0, err
		},
	}
}

// NewNetFlow9Listener builds a Listener decoding NetFlow v9 datagrams,
// sharing cache across every datagram this listener receives (templates
// are per-exporter — see TemplateCache's doc comment).
func NewNetFlow9Listener(addr, node string, cache *TemplateCache) *Listener {
	return &Listener{
		Addr: addr, Node: node,
		Decode: func(data []byte, node string, _ int64, exporterKey string) ([]Record, int, error) {
			return DecodeNetFlow9(data, node, exporterKey, cache)
		},
	}
}

// NewNetFlowListener builds a Listener that dispatches each datagram to
// DecodeNetFlow5 or DecodeNetFlow9 by sniffing the wire header's version
// field (both protocols conventionally share one port, 2055 — a real
// collector commonly does exactly this rather than requiring an operator
// to configure two separate ports for two versions no single exporter ever
// speaks simultaneously). Any other version value is rejected with
// ErrUnsupportedVersion, decodeSafely's usual defensive-skip path.
func NewNetFlowListener(addr, node string, cache *TemplateCache) *Listener {
	return &Listener{
		Addr: addr, Node: node,
		Decode: func(data []byte, node string, now int64, exporterKey string) ([]Record, int, error) {
			if len(data) < 2 {
				return nil, 0, fmt.Errorf("netflow: datagram too short to determine version: %w", ErrMalformed)
			}
			switch version := uint16(data[0])<<8 | uint16(data[1]); version {
			case 5:
				records, err := DecodeNetFlow5(data, node)
				return records, 0, err
			case 9:
				return DecodeNetFlow9(data, node, exporterKey, cache)
			default:
				return nil, 0, fmt.Errorf("netflow: version %d: %w", version, ErrUnsupportedVersion)
			}
		},
	}
}

// NewIPFIXListener builds a Listener decoding IPFIX datagrams, sharing
// cache across every datagram this listener receives.
func NewIPFIXListener(addr, node string, cache *TemplateCache) *Listener {
	return &Listener{
		Addr: addr, Node: node,
		Decode: func(data []byte, node string, _ int64, exporterKey string) ([]Record, int, error) {
			return DecodeIPFIX(data, node, exporterKey, cache)
		},
	}
}

// Run binds l.Addr and decodes datagrams until ctx is cancelled, calling
// ingest with every batch of successfully decoded Records. It returns nil
// on a clean shutdown (ctx cancellation) or a wrapped error if the initial
// bind fails; a malformed/truncated datagram or a decoder that panics (a
// second, structural guarantee on top of every decoder's own bounds-checked
// parsing — see decodeSafely) is logged and skipped, never fatal to the
// loop — the defensive-parsing contract this package's doc comment
// documents, applied at the listener/goroutine level (AC2's "never blocks
// the listener goroutine").
func (l *Listener) Run(ctx context.Context, ingest func(ctx context.Context, records []Record)) error {
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := l.Now
	if now == nil {
		now = time.Now
	}
	maxSize := l.MaxDatagramSize
	if maxSize <= 0 {
		maxSize = DefaultMaxDatagramSize
	}

	conn, err := net.ListenPacket("udp", l.Addr)
	if err != nil {
		return fmt.Errorf("flow: binding UDP listener on %s: %w", l.Addr, err)
	}
	defer func() { _ = conn.Close() }()
	if l.ready != nil {
		close(l.ready)
	}

	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close() // unblocks the ReadFrom loop below
		case <-closed:
		}
	}()
	defer close(closed)

	buf := make([]byte, maxSize)
	for {
		n, addr, readErr := conn.ReadFrom(buf)
		if readErr != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown: conn.Close() above caused this
			}
			logger.Warn("flow: udp read error", "addr", l.Addr, "error", readErr)
			continue
		}

		datagram := make([]byte, n)
		copy(datagram, buf[:n])
		exporterKey := ""
		if addr != nil {
			exporterKey = addr.String()
		}

		records, dropped, decErr := l.decodeSafely(datagram, exporterKey, now().Unix())
		if decErr != nil {
			logger.Debug("flow: dropping malformed/undecodable datagram", "addr", l.Addr, "exporter", exporterKey, "error", decErr)
		}
		if dropped > 0 {
			logger.Debug("flow: dropped records with no cached template", "addr", l.Addr, "exporter", exporterKey, "dropped", dropped)
		}
		if len(records) > 0 && ingest != nil {
			ingest(ctx, records)
		}
	}
}

// decodeSafely wraps l.Decode with a recover(): every decoder in this
// package is itself built never to panic (breader's bounds-checked reads
// throughout), but Run's loop must survive no matter what a future decoder
// change (or an input this package's own reasoning about "always
// recoverable" turns out to have missed) throws at it — see this method's
// callers' doc comment.
func (l *Listener) decodeSafely(data []byte, exporterKey string, now int64) (records []Record, dropped int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("flow: decoder panicked (recovered): %v", r)
		}
	}()
	return l.Decode(data, l.Node, now, exporterKey)
}
