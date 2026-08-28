// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLFileSink_WritesOneObjectPerLineAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.jsonl")

	sink, openErr := NewJSONLFileSink(path)
	if openErr != nil {
		t.Fatalf("NewJSONLFileSink: %v", openErr)
	}

	auditEv := NewAuditEvent(AuditInput{ID: 1, Username: "bob", Action: "login", Result: "success"})
	findingEv := NewFindingEvent(FindingInput{ID: "health:x", Source: "health", Check: "x", Severity: "warning", Transition: TransitionNew, Refs: []string{"vmbr0"}})

	if sendErr := sink.Send(context.Background(), auditEv); sendErr != nil {
		t.Fatalf("Send audit: %v", sendErr)
	}
	if sendErr := sink.Send(context.Background(), findingEv); sendErr != nil {
		t.Fatalf("Send finding: %v", sendErr)
	}
	if closeErr := sink.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	// Re-open (a second Exporter lifetime, e.g. after a daemon restart)
	// and send a third event — NewJSONLFileSink must APPEND, never
	// truncate.
	sink2, reopenErr := NewJSONLFileSink(path)
	if reopenErr != nil {
		t.Fatalf("NewJSONLFileSink (reopen): %v", reopenErr)
	}
	thirdEv := NewAuditEvent(AuditInput{ID: 2, Username: "carol", Action: "logout", Result: "success"})
	if sendErr := sink2.Send(context.Background(), thirdEv); sendErr != nil {
		t.Fatalf("Send after reopen: %v", sendErr)
	}
	if closeErr := sink2.Close(); closeErr != nil {
		t.Fatalf("Close (reopen): %v", closeErr)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (append must not truncate): %q", len(lines), string(data))
	}

	var rec1 jsonlRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec1); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if rec1.Kind != KindAudit || rec1.Username != "bob" || rec1.Action != "login" {
		t.Fatalf("line 1 = %+v, want the first audit event's fields", rec1)
	}

	var rec2 jsonlRecord
	if err := json.Unmarshal([]byte(lines[1]), &rec2); err != nil {
		t.Fatalf("line 2 is not valid JSON: %v", err)
	}
	if rec2.Kind != KindFinding || rec2.FindingID != "health:x" || rec2.Transition != TransitionNew {
		t.Fatalf("line 2 = %+v, want the finding event's fields", rec2)
	}
	if len(rec2.Refs) != 1 || rec2.Refs[0] != "vmbr0" {
		t.Fatalf("line 2 refs = %v, want [vmbr0]", rec2.Refs)
	}

	var rec3 jsonlRecord
	if err := json.Unmarshal([]byte(lines[2]), &rec3); err != nil {
		t.Fatalf("line 3 is not valid JSON: %v", err)
	}
	if rec3.Username != "carol" {
		t.Fatalf("line 3 = %+v, want the post-reopen event", rec3)
	}
}

func TestJSONLNetSink_TCP_NewlineDelimited(t *testing.T) {
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

	sink := NewJSONLNetSink("tcp", ln.Addr().String())
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewFindingEvent(FindingInput{ID: "health:y", Severity: "error", Transition: TransitionResolved})
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

	line, readErr := bufio.NewReader(conn).ReadString('\n')
	if readErr != nil {
		t.Fatalf("reading line: %v", readErr)
	}
	var rec jsonlRecord
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &rec); err != nil {
		t.Fatalf("line is not valid JSON: %v (%q)", err, line)
	}
	if rec.FindingID != "health:y" || rec.Transition != TransitionResolved || rec.Severity != "error" {
		t.Fatalf("record = %+v, want the sent finding event's fields", rec)
	}
}

func TestJSONLNetSink_FarEndDown_SendFails(t *testing.T) {
	ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("listening: %v", listenErr)
	}
	addr := ln.Addr().String()
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("closing listener: %v", closeErr)
	}

	sink := NewJSONLNetSink("tcp", addr)
	t.Cleanup(func() { _ = sink.Close() })

	ev := NewAuditEvent(AuditInput{ID: 1, Result: "success"})
	if sendErr := sink.Send(context.Background(), ev); sendErr == nil {
		t.Fatal("Send succeeded against a closed port, want an error")
	}
}
