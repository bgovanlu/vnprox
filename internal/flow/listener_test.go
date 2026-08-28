// SPDX-License-Identifier: Apache-2.0

package flow

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// listenerRecvTimeout bounds how long a test waits for a sent datagram to
// reach the ingest callback. Every test below waits on l.ready
// (runListenerForTest) before sending, which removes this file's original
// source of flakiness: a test's own outside-in readiness probe racing
// Run's single bind attempt for the same port and occasionally winning it,
// starving Run and leaving nothing ever listening. What remains is only
// real (loopback, effectively instant) delivery latency, so this can stay
// modest.
const listenerRecvTimeout = 5 * time.Second

// TestListener_NetFlow5_EndToEnd binds a real loopback UDP listener,
// sends the golden netflow5_basic.bin fixture at it, and asserts the
// decoded records reach the ingest callback — the wiring golden decode
// tests (decode_test.go) don't exercise: the actual net.ListenPacket loop.
func TestListener_NetFlow5_EndToEnd(t *testing.T) {
	l := NewNetFlow5Listener("", "pve1")

	var mu sync.Mutex
	var got []Record
	received := make(chan struct{}, 1)

	boundAddr, cancel, runErr := runListenerForTest(t, l, func(_ context.Context, records []Record) {
		mu.Lock()
		got = append(got, records...)
		mu.Unlock()
		select {
		case received <- struct{}{}:
		default:
		}
	})
	defer cancel()

	sendUntilReceived(t, boundAddr, fixture(t, "netflow5_basic.bin"), received)

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned an error after clean shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].SrcIP != "10.0.0.5" {
		t.Errorf("first record SrcIP = %q, want 10.0.0.5", got[0].SrcIP)
	}
}

// TestListener_NetFlowCombined_DispatchesByVersion proves NewNetFlowListener
// correctly routes a v5 datagram to DecodeNetFlow5 and a v9 datagram to
// DecodeNetFlow9 off the same bound port, by version-sniffing alone.
func TestListener_NetFlowCombined_DispatchesByVersion(t *testing.T) {
	cache := NewTemplateCache(nil)
	l := NewNetFlowListener("", "pve1", cache)

	var mu sync.Mutex
	var got []Record
	received := make(chan struct{}, 4)

	boundAddr, cancel, runErr := runListenerForTest(t, l, func(_ context.Context, records []Record) {
		mu.Lock()
		got = append(got, records...)
		mu.Unlock()
		received <- struct{}{}
	})
	defer cancel()

	sendUntilReceived(t, boundAddr, fixture(t, "netflow5_basic.bin"), received)

	// Both v9 datagrams must be sent from the same source port (the
	// TemplateCache key includes the exporter address) — dial once and
	// reuse the connection, rather than sendUDP's own one-shot dial per
	// call, which would give each datagram its own ephemeral port and make
	// the data datagram land under a template the cache never saw.
	v9Conn, err := net.Dial("udp", boundAddr)
	if err != nil {
		t.Fatalf("dialing %s: %v", boundAddr, err)
	}
	defer func() { _ = v9Conn.Close() }()
	if _, err := v9Conn.Write(fixture(t, "netflow9_template.bin")); err != nil {
		t.Fatalf("writing template datagram: %v", err)
	}
	if _, err := v9Conn.Write(fixture(t, "netflow9_data.bin")); err != nil {
		t.Fatalf("writing data datagram: %v", err)
	}
	select {
	case <-received:
	case <-time.After(listenerRecvTimeout):
		t.Fatal("timed out waiting for the v9 data datagram")
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned an error after clean shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var v5Count, v9Count int
	for _, r := range got {
		switch r.Source {
		case SourceNetFlow5:
			v5Count++
		case SourceNetFlow9:
			v9Count++
		}
	}
	if v5Count != 2 {
		t.Errorf("v5 records = %d, want 2", v5Count)
	}
	if v9Count != 1 {
		t.Errorf("v9 records = %d, want 1", v9Count)
	}
}

// TestListener_MalformedDatagram_NeverBlocks sends a burst of garbage
// datagrams (including a zero-length one) followed by a valid golden
// datagram, asserting the listener goroutine keeps servicing new datagrams
// throughout — AC2's "never blocks the listener goroutine" at the
// net.ListenPacket level.
func TestListener_MalformedDatagram_NeverBlocks(t *testing.T) {
	l := NewSFlowListener("", "pve1")

	var mu sync.Mutex
	var got []Record
	received := make(chan struct{}, 1)

	boundAddr, cancel, runErr := runListenerForTest(t, l, func(_ context.Context, records []Record) {
		mu.Lock()
		got = append(got, records...)
		mu.Unlock()
		select {
		case received <- struct{}{}:
		default:
		}
	})
	defer cancel()

	garbage := [][]byte{
		{},
		{0x00},
		{0xff, 0xff, 0xff, 0xff},
		make([]byte, 200), // all zero bytes: parses as version 0, rejected
	}
	for _, g := range garbage {
		sendUDP(t, boundAddr, g)
	}
	// A brief pause lets the listener goroutine process the garbage before
	// the real datagram — not required for correctness (the channel wait
	// below is what actually synchronizes), just keeps the garbage
	// deliveries ordered ahead of the valid one on a loopback socket.
	time.Sleep(20 * time.Millisecond)

	sendUntilReceived(t, boundAddr, fixture(t, "sflow5_basic.bin"), received)

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned an error after clean shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 (from the one valid datagram)", len(got))
	}
}

// TestListener_Run_BindFailure asserts Run returns a wrapped error (not a
// panic) when the address can't be bound.
func TestListener_Run_BindFailure(t *testing.T) {
	l := NewSFlowListener("not-a-valid-address", "pve1")
	err := l.Run(context.Background(), func(context.Context, []Record) {})
	if err == nil {
		t.Fatal("expected an error binding an invalid address")
	}
}

// runListenerForTest reserves a free loopback port, points l at it, starts
// l.Run in a background goroutine with ingest, and blocks until l has
// actually bound the port (via the test-only l.ready channel — see
// Listener.ready's doc comment) before returning — removing the race a
// test polling l.Addr from the outside would otherwise have against Run's
// own single bind attempt (two independent binders racing for the same
// port can make the *test's* probe win and starve Run's).
func runListenerForTest(t *testing.T, l *Listener, ingest func(context.Context, []Record)) (addr string, cancel context.CancelFunc, runErr <-chan error) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	boundAddr := conn.LocalAddr().String()
	_ = conn.Close()
	l.Addr = boundAddr
	l.ready = make(chan struct{})

	runCtx, runCancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- l.Run(runCtx, ingest) }()

	select {
	case <-l.ready:
	case <-time.After(listenerRecvTimeout):
		runCancel()
		t.Fatal("timed out waiting for the listener to bind " + boundAddr)
	}
	return boundAddr, runCancel, errCh
}

// sendUntilReceived sends data to addr once and waits up to
// listenerRecvTimeout for a signal on received.
func sendUntilReceived(t *testing.T, addr string, data []byte, received <-chan struct{}) {
	t.Helper()
	sendUDP(t, addr, data)
	select {
	case <-received:
	case <-time.After(listenerRecvTimeout):
		t.Fatal("timed out waiting for the listener to ingest the datagram")
	}
}

func sendUDP(t *testing.T, addr string, data []byte) {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dialing %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("writing datagram: %v", err)
	}
}
