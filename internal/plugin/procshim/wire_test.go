package procshim

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/bgovanlu/vnprox/internal/plugin"
)

// staticTiles is a trivial DashboardTileProvider for transport tests.
type staticTiles struct{}

func (staticTiles) Tiles(_ context.Context) ([]plugin.Tile, error) {
	return []plugin.Tile{{ID: "t", Value: "1"}}, nil
}

func TestServe_RoundTripAndUnknownMethod(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	go func() { _ = Serve(context.Background(), serverConn, Impls{DashboardTiles: staticTiles{}}) }()
	defer func() { _ = clientConn.Close() }()

	// Known method round-trips a typed result.
	if err := writeFrame(clientConn, request{Method: methodTileTiles}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp response
	if err := readFrame(clientConn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var out tileTilesResp
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Tiles) != 1 || out.Tiles[0].ID != "t" {
		t.Fatalf("tiles = %+v", out.Tiles)
	}

	// Unknown method is a hard error response, never a hang or a crash.
	if err := writeFrame(clientConn, request{Method: "bogus.method"}); err != nil {
		t.Fatalf("write2: %v", err)
	}
	var resp2 response
	if err := readFrame(clientConn, &resp2); err != nil {
		t.Fatalf("read2: %v", err)
	}
	if resp2.Error == "" {
		t.Fatal("unknown method did not return an error response")
	}

	// A call against an unimplemented point (no FlowIngestor) also errors cleanly.
	if err := writeFrame(clientConn, request{Method: methodFlowIngest, Params: json.RawMessage(`{"node":"n","payload":""}`)}); err != nil {
		t.Fatalf("write3: %v", err)
	}
	var resp3 response
	if err := readFrame(clientConn, &resp3); err != nil {
		t.Fatalf("read3: %v", err)
	}
	if resp3.Error == "" {
		t.Fatal("call to unimplemented point did not return an error response")
	}
}
