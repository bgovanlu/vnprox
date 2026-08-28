// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"testing"
	"time"
)

// spyFlowReader is an in-memory FlowReader stand-in for T-1002's peer flow
// fan-out tests, mirroring spyAuditReader's shape exactly.
type spyFlowReader struct {
	err        error
	pages      map[string]flowPageResponse
	lastFilter FlowFilter
	lastLimit  int
}

func newSpyFlowReader() *spyFlowReader {
	return &spyFlowReader{pages: map[string]flowPageResponse{}}
}

func (r *spyFlowReader) ListFlowPage(_ context.Context, filter FlowFilter, cursor string, limit int) ([]FlowRecord, string, error) {
	r.lastFilter = filter
	r.lastLimit = limit
	if r.err != nil {
		return nil, "", r.err
	}
	page := r.pages[cursor]
	return page.Items, page.NextCursor, nil
}

func TestClient_Flows_RoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	flowR := newSpyFlowReader()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Flows:   flowR,
		Version: "test",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(),
		Now: func() time.Time { return now },
	})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	want := []FlowRecord{
		{ID: 1, At: 100, Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Proto: 6, Source: "netflow5"},
	}
	flowR.pages[""] = flowPageResponse{Items: want, NextCursor: "next-token"}

	filter := FlowFilter{Guest: "bridge:pve1:vmbr0", VLAN: 100, Port: 443, Proto: 6, FromTs: 1, ToTs: 2, Subnet: "10.0.0.0/24"}
	items, next, err := client.Flows(t.Context(), p, filter, "", 25)
	if err != nil {
		t.Fatalf("Flows: %v", err)
	}
	if len(items) != 1 || items[0].SrcIP != "10.0.0.1" {
		t.Fatalf("Flows items = %+v", items)
	}
	if next != "next-token" {
		t.Errorf("nextCursor = %q, want next-token", next)
	}
	if flowR.lastLimit != 25 {
		t.Errorf("server-observed limit = %d, want 25", flowR.lastLimit)
	}
	if flowR.lastFilter != filter {
		t.Errorf("server-observed filter = %+v, want %+v", flowR.lastFilter, filter)
	}
}

func TestClient_Flows_CursorForwarded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	flowR := newSpyFlowReader()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Flows: flowR, Version: "test",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(),
		Now: func() time.Time { return now },
	})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	flowR.pages["resume-here"] = flowPageResponse{Items: []FlowRecord{{ID: 2, At: 50}}, NextCursor: ""}

	items, next, err := client.Flows(t.Context(), p, FlowFilter{}, "resume-here", 10)
	if err != nil {
		t.Fatalf("Flows: %v", err)
	}
	if len(items) != 1 || items[0].ID != 2 {
		t.Fatalf("items = %+v", items)
	}
	if next != "" {
		t.Errorf("nextCursor = %q, want empty", next)
	}
}

func TestServer_UnconfiguredFlows503s(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(),
		Now: func() time.Time { return now },
	})
	p := Peer{Node: "solo", Addr: ts.Listener.Addr().String()}

	if _, _, err := client.Flows(t.Context(), p, FlowFilter{}, "", 10); err == nil {
		t.Fatal("expected an error with no FlowReader configured")
	}
}
