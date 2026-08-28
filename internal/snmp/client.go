// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// maxMessageSize bounds both what this client will send and what it will
// accept on decode — larger than any real SNMP-over-UDP datagram (agents
// conventionally cap responses well under the classic 1500-byte Ethernet
// MTU; some support jumbo responses up to the UDP maximum), small enough to
// bound a single allocation for a hostile/misbehaving agent's reply.
const maxMessageSize = 65507 // max UDP payload over IPv4

// DefaultPort is SNMP's registered UDP port.
const DefaultPort = 161

// DefaultTimeout is used when Client.Timeout is zero.
const DefaultTimeout = 3 * time.Second

// Client is a single-target SNMPv2c client — read-only (see doc.go). Not
// safe for concurrent Get/GetBulk calls from multiple goroutines against the
// same *Client (each call owns the socket it dials for its own duration);
// internal/ifcounters constructs one per poll per switch rather than sharing
// an instance across goroutines.
type Client struct {
	conn      net.Conn
	community []byte
	timeout   time.Duration
	nextReqID atomic.Uint32
}

// Dial opens a UDP "connection" (net.Dial's usual meaning for UDP: it fixes
// the remote address and lets subsequent Write/Read target it, no handshake
// occurs) to addr ("host:port") for community. timeout is applied per
// request as a read deadline; zero uses DefaultTimeout.
func Dial(addr string, community []byte, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("snmp: dialing %s: %w", addr, err)
	}
	c := &Client{
		conn: conn,
		// Copied, not aliased: the caller's byte slice (freshly decrypted
		// from switch_snmp_targets.community_enc) must not be retained
		// beyond what this Client needs it for, and must not be mutable out
		// from under it.
		community: append([]byte(nil), community...),
		timeout:   timeout,
	}
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err == nil {
		c.nextReqID.Store(binary.BigEndian.Uint32(seed[:]))
	}
	return c, nil
}

// Close releases the underlying socket.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) requestID() int32 {
	// SNMP request-ids are a 32-bit signed field; mask off the sign bit so
	// every generated value decodes back as non-negative (cosmetic — a
	// negative request-id round-trips fine, but agents/log lines read
	// oddly with one).
	return int32(c.nextReqID.Add(1) & 0x7fffffff) //nolint:gosec // truncation is intentional: request-id only needs local uniqueness, not full uint32 entropy.
}

// Get issues a single GetRequest for oids and returns the response varbinds
// in the order the agent returned them (normally the order requested, but
// this client does not assume that — see internal/ifcounters, which indexes
// results by OID rather than by position).
func (c *Client) Get(ctx context.Context, oids []OID) ([]Varbind, error) {
	return c.request(ctx, GetRequestPDU, 0, 0, oids)
}

// GetNext issues a single GetNextRequest for oids.
func (c *Client) GetNext(ctx context.Context, oids []OID) ([]Varbind, error) {
	return c.request(ctx, GetNextRequestPDU, 0, 0, oids)
}

// GetBulk issues a single GetBulkRequest. maxRepetitions bounds how many
// rows the agent may return per requested OID; internal/ifcounters caps this
// itself before calling in (defensive: never trust a caller-supplied bound
// alone), but this method does not silently clamp — an unreasonable value is
// the caller's bug to fix, not this package's to paper over.
func (c *Client) GetBulk(ctx context.Context, nonRepeaters, maxRepetitions int32, oids []OID) ([]Varbind, error) {
	return c.request(ctx, GetBulkRequestPDU, nonRepeaters, maxRepetitions, oids)
}

func (c *Client) request(ctx context.Context, pduType PDUType, field2, field3 int32, oids []OID) (varbinds []Varbind, err error) {
	// Belt-and-braces per doc.go: even though every decode path below is
	// individually bounds-checked, a panic anywhere in it (a bug, or a
	// bound this review missed) must still come back as an error, never
	// crash the poller goroutine that called in.
	defer func() {
		if r := recover(); r != nil {
			varbinds, err = nil, fmt.Errorf("snmp: decode panic recovered: %v", r)
		}
	}()

	reqID := c.requestID()
	var payload []byte
	switch pduType {
	case GetRequestPDU:
		payload, err = EncodeGetRequest(c.community, reqID, oids)
	case GetNextRequestPDU:
		payload, err = EncodeGetNextRequest(c.community, reqID, oids)
	case GetBulkRequestPDU:
		payload, err = EncodeGetBulkRequest(c.community, reqID, field2, field3, oids)
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnsupportedPDUType, byte(pduType))
	}
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if setErr := c.conn.SetDeadline(deadline); setErr != nil {
		return nil, fmt.Errorf("snmp: setting deadline: %w", setErr)
	}

	if _, writeErr := c.conn.Write(payload); writeErr != nil {
		return nil, fmt.Errorf("snmp: writing request: %w", writeErr)
	}

	buf := make([]byte, maxMessageSize)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("snmp: reading response: %w", err)
	}

	msg, err := DecodeMessage(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("snmp: decoding response: %w", err)
	}
	if msg.Version != version2c {
		return nil, ErrVersionMismatch
	}
	if msg.PDU.Type != GetResponsePDU {
		return nil, fmt.Errorf("snmp: response PDU type is %s, want GetResponse", msg.PDU.Type)
	}
	if msg.PDU.RequestID != reqID {
		return nil, ErrRequestIDMismatch
	}
	if msg.PDU.Field2 != 0 {
		return nil, &AgentError{Status: msg.PDU.Field2, Index: msg.PDU.Field3}
	}
	return msg.PDU.Varbinds, nil
}
