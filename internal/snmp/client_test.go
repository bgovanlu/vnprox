// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// fakeAgent is a table-driven local UDP responder standing in for a switch's
// SNMP agent — CLAUDE.md's instruction for this card ("table-driven Go
// tests against a local UDP responder, never a real device"). It decodes
// the incoming request just enough to read the request-id and echoes back
// whatever responder function the test configured, so tests can exercise
// Client.Get/GetBulk's full encode -> UDP round trip -> decode path without
// ever touching a network vnprox doesn't control.
type fakeAgent struct {
	conn *net.UDPConn
	addr string
}

func startFakeAgent(t *testing.T, handle func(reqID int32) []byte) *fakeAgent {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	a := &fakeAgent{conn: conn, addr: conn.LocalAddr().String()}
	go func() {
		buf := make([]byte, maxMessageSize)
		for {
			n, raddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // conn closed by test cleanup
			}
			msg, err := DecodeMessage(buf[:n])
			if err != nil {
				continue
			}
			resp := handle(msg.PDU.RequestID)
			if resp == nil {
				continue // simulate a dropped/ignored request (timeout case)
			}
			_, _ = conn.WriteToUDP(resp, raddr)
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return a
}

func TestClient_Get_RoundTrip(t *testing.T) {
	wantOID := MustParseOID("1.3.6.1.2.1.2.2.1.14.1")
	agent := startFakeAgent(t, func(reqID int32) []byte {
		return buildGetResponse(reqID, tagCounter32, []byte{0x00, 0x00, 0x00, 0x2a}, wantOID)
	})
	client, err := Dial(agent.addr, []byte("public"), time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	vbs, err := client.Get(context.Background(), []OID{wantOID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("varbinds = %d, want 1", len(vbs))
	}
	if vbs[0].Value.Kind != KindCounter32 || vbs[0].Value.UInt != 42 {
		t.Errorf("got %+v, want Counter32(42)", vbs[0].Value)
	}
}

func TestClient_GetBulk_RoundTrip(t *testing.T) {
	base := MustParseOID("1.3.6.1.2.1.2.2.1.2")
	agent := startFakeAgent(t, func(reqID int32) []byte {
		// Simulate a 3-row table walk response.
		var vbList []byte
		for i := uint32(1); i <= 3; i++ {
			var vb []byte
			vb = appendOID(vb, base.Append(i))
			vb = appendOctetString(vb, []byte("eth"+string(rune('0'+i))))
			vbList = appendTLV(vbList, tagSequence, vb)
		}
		var pduBody []byte
		pduBody = appendInteger(pduBody, tagInteger, reqID)
		pduBody = appendInteger(pduBody, tagInteger, 0)
		pduBody = appendInteger(pduBody, tagInteger, 0)
		pduBody = appendTLV(pduBody, tagSequence, vbList)
		pduBytes := appendTLV(nil, byte(GetResponsePDU), pduBody)
		var body []byte
		body = appendInteger(body, tagInteger, version2c)
		body = appendOctetString(body, []byte("public"))
		body = append(body, pduBytes...)
		return appendTLV(nil, tagSequence, body)
	})
	client, err := Dial(agent.addr, []byte("public"), time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	vbs, err := client.GetBulk(context.Background(), 0, 10, []OID{base})
	if err != nil {
		t.Fatalf("GetBulk: %v", err)
	}
	if len(vbs) != 3 {
		t.Fatalf("varbinds = %d, want 3", len(vbs))
	}
	for i, vb := range vbs {
		if !vb.Name.HasPrefix(base) {
			t.Errorf("varbind %d name %s does not have prefix %s", i, vb.Name, base)
		}
	}
}

func TestClient_Get_TimeoutOnNoResponse(t *testing.T) {
	agent := startFakeAgent(t, func(int32) []byte { return nil }) // never responds
	client, err := Dial(agent.addr, []byte("public"), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.Get(context.Background(), []OID{MustParseOID("1.3.6.1.2.1.1.1.0")})
	if err == nil {
		t.Fatal("Get succeeded against an agent that never responds, want a timeout error")
	}
}

func TestClient_Get_AgentErrorStatus(t *testing.T) {
	oid := MustParseOID("1.3.6.1.2.1.2.2.1.14.99")
	agent := startFakeAgent(t, func(reqID int32) []byte {
		var pduBody []byte
		pduBody = appendInteger(pduBody, tagInteger, reqID)
		pduBody = appendInteger(pduBody, tagInteger, 2) // noSuchName
		pduBody = appendInteger(pduBody, tagInteger, 1)
		var vb []byte
		vb = appendOID(vb, oid)
		vb = appendNull(vb)
		vbList := appendTLV(nil, tagSequence, vb)
		pduBody = appendTLV(pduBody, tagSequence, vbList)
		pduBytes := appendTLV(nil, byte(GetResponsePDU), pduBody)
		var body []byte
		body = appendInteger(body, tagInteger, version2c)
		body = appendOctetString(body, []byte("public"))
		body = append(body, pduBytes...)
		return appendTLV(nil, tagSequence, body)
	})
	client, err := Dial(agent.addr, []byte("public"), time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.Get(context.Background(), []OID{oid})
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("Get = %v, want *AgentError", err)
	}
	if agentErr.Status != 2 || agentErr.Index != 1 {
		t.Errorf("AgentError = %+v, want Status=2 Index=1", agentErr)
	}
}

func TestClient_Get_RequestIDMismatchIgnored(t *testing.T) {
	// The agent responds with a request-id that doesn't match — Client must
	// reject it rather than hand back a mismatched response as if it were
	// real data (guards against a stale/spoofed UDP datagram).
	oid := MustParseOID("1.3.6.1.2.1.1.1.0")
	agent := startFakeAgent(t, func(reqID int32) []byte {
		return buildGetResponse(reqID+1, tagOctetString, []byte("wrong"), oid)
	})
	client, err := Dial(agent.addr, []byte("public"), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.Get(context.Background(), []OID{oid})
	if !errors.Is(err, ErrRequestIDMismatch) {
		t.Fatalf("Get = %v, want ErrRequestIDMismatch", err)
	}
}

func TestClient_CommunityNeverAppearsInPublicError(t *testing.T) {
	// Belt-and-braces: dialing a bad address must not leak the community
	// string into the returned error text.
	_, err := Dial("256.256.256.256:161", []byte("s3cr3t-community"), time.Second)
	if err == nil {
		t.Fatal("Dial with invalid address unexpectedly succeeded")
	}
	if got := err.Error(); containsFold(got, "s3cr3t-community") {
		t.Errorf("Dial error leaked the community string: %q", got)
	}
}
