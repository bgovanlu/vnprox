// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readOctetCountedFrame reads one RFC 6587 §3.4.1 octet-counted frame
// ("MSGLEN SP SYSLOG-MSG") off r — the framing NewSyslogSink uses for
// tcp/unix, per sink_syslog.go's doc comment.
func readOctetCountedFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	lenStr, err := r.ReadString(' ')
	if err != nil {
		t.Fatalf("reading frame length: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(lenStr))
	if err != nil {
		t.Fatalf("parsing frame length %q: %v", lenStr, err)
	}
	buf := make([]byte, n)
	if _, err := readFull(r, buf); err != nil {
		t.Fatalf("reading %d-byte frame: %v", n, err)
	}
	return string(buf)
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestSyslogSink_TCP_WellFormedRFC5424Frame proves NewSyslogSink over TCP
// emits a message a real listener can parse: a valid octet-counting frame
// wrapping an RFC 5424 message whose SD-ELEMENT carries this package's
// documented field mapping (doc.go).
func TestSyslogSink_TCP_WellFormedRFC5424Frame(t *testing.T) {
	ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listening: %v", listenErr)
	}
	t.Cleanup(func() { _ = ln.Close() })

	connCh := make(chan net.Conn, 1)
	go func() {
		c, acceptErr := ln.Accept()
		if acceptErr == nil {
			connCh <- c
		}
	}()

	sink := NewSyslogSink("tcp", ln.Addr().String(), 16 /* local0 */)
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewAuditEvent(AuditInput{
		ID: 42, Username: "alice", Action: "changeset.apply", Target: "vmbr0",
		ChangesetID: "cs-1", Result: "denied", IP: "192.168.1.9",
	})
	if sendErr := sink.Send(context.Background(), ev); sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}

	var conn net.Conn
	select {
	case conn = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never accepted a connection")
	}
	t.Cleanup(func() { _ = conn.Close() })

	msg := readOctetCountedFrame(t, bufio.NewReader(conn))
	if !strings.HasPrefix(msg, "<") {
		t.Fatalf("message does not start with a PRI part: %q", msg)
	}
	// Facility 16 (local0), severity 4 (Warning, from Result "denied") ->
	// PRI = 16*8+4 = 132.
	if !strings.HasPrefix(msg, "<132>1 ") {
		t.Fatalf("PRI/VERSION = %q, want prefix \"<132>1 \"", msg[:min(20, len(msg))])
	}
	for _, want := range []string{
		"vnproxAudit@32473", `id="42"`, `username="alice"`,
		`action="changeset.apply"`, `target="vmbr0"`, `changesetId="cs-1"`,
		`result="denied"`, `ip="192.168.1.9"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

// TestSyslogSink_FarEndDown_SendFails proves Send surfaces a real dial
// error when nothing is listening — the transport-level half of the
// "far end down" acceptance case (Exporter's own handling of that error is
// exporter_test.go's job).
func TestSyslogSink_FarEndDown_SendFails(t *testing.T) {
	// Reserve a port, then close the listener immediately so nothing is
	// listening on it — a fast, deterministic "connection refused".
	ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listening: %v", listenErr)
	}
	addr := ln.Addr().String()
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("closing listener: %v", closeErr)
	}

	sink := NewSyslogSink("tcp", addr, 16)
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewAuditEvent(AuditInput{ID: 1, Result: "success"})
	if sendErr := sink.Send(context.Background(), ev); sendErr == nil {
		t.Fatal("Send succeeded against a closed port, want an error")
	}
}

// TestSyslogSink_Reconnect_SucceedsOnceListenerStarts proves netSink's
// lazy-reconnect contract for the syslog transport specifically: a Send
// against a not-yet-listening address fails, and a LATER Send against the
// same sink succeeds once a listener exists — without recreating the Sink.
func TestSyslogSink_Reconnect_SucceedsOnceListenerStarts(t *testing.T) {
	ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listening: %v", listenErr)
	}
	addr := ln.Addr().String()
	if closeErr := ln.Close(); closeErr != nil { // nothing listening yet
		t.Fatalf("closing listener: %v", closeErr)
	}

	sink := NewSyslogSink("tcp", addr, 16)
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewAuditEvent(AuditInput{ID: 1, Result: "success"})
	if sendErr := sink.Send(context.Background(), ev); sendErr == nil {
		t.Fatal("Send succeeded before the listener existed, want an error")
	}

	ln2, listen2Err := net.Listen("tcp", addr)
	if listen2Err != nil {
		t.Skipf("could not re-bind %s (port reuse timing): %v", addr, listen2Err)
	}
	t.Cleanup(func() { _ = ln2.Close() })
	connCh := make(chan net.Conn, 1)
	go func() {
		c, acceptErr := ln2.Accept()
		if acceptErr == nil {
			connCh <- c
		}
	}()

	if sendErr := sink.Send(context.Background(), ev); sendErr != nil {
		t.Fatalf("Send after listener started: %v", sendErr)
	}
	select {
	case <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never accepted a connection after reconnect")
	}
}

// TestSyslogSink_UDP_NoFraming proves UDP mode sends the bare RFC 5424
// message as a single datagram, with no RFC 6587 length prefix.
func TestSyslogSink_UDP_NoFraming(t *testing.T) {
	pc, listenErr := net.ListenPacket("udp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listening: %v", listenErr)
	}
	t.Cleanup(func() { _ = pc.Close() })

	sink := NewSyslogSink("udp", pc.LocalAddr().String(), 16)
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewAuditEvent(AuditInput{ID: 7, Result: "success"})
	if sendErr := sink.Send(context.Background(), ev); sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}

	buf := make([]byte, 4096)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, readErr := pc.ReadFrom(buf)
	if readErr != nil {
		t.Fatalf("reading datagram: %v", readErr)
	}
	msg := string(buf[:n])
	if !strings.HasPrefix(msg, "<") {
		t.Fatalf("datagram is not a bare RFC5424 message (framed?): %q", msg)
	}
	if strings.ContainsAny(msg[:min(6, len(msg))], "0123456789") && strings.Contains(msg[:min(6, len(msg))], " ") {
		// A framed message would start "N <pri>..." — a bare one starts
		// directly with "<pri>...", which the HasPrefix check above
		// already confirms; this is just an extra sanity guard against a
		// leading digit sneaking in.
		t.Fatalf("datagram looks length-prefixed: %q", msg[:min(20, len(msg))])
	}
}
