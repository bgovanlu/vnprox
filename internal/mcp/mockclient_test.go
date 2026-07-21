package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// mockClient is the JSON-RPC test harness (T-1701's "Mock MCP client" fixture):
// it drives a real Server.ServeStdio instance over an in-memory pipe pair,
// speaking newline-delimited JSON-RPC exactly as a stdio MCP client would. Each
// call blocks for the matching response id.
type mockClient struct {
	serveErr error
	t        *testing.T
	toSrv    *io.PipeWriter
	fromSrv  *bufio.Reader
	closer   *io.PipeReader
	serveWG  sync.WaitGroup
	nextID   int
	serveMu  sync.Mutex
}

// newMockClient starts srv.ServeStdio for session on an in-memory transport and
// returns a client bound to it. The returned cancel stops the serve loop.
func newMockClient(t *testing.T, srv *Server, session *Session) (*mockClient, context.CancelFunc) {
	t.Helper()
	serverReads, clientToServer := io.Pipe() // client writes -> server reads
	clientReads, serverWrites := io.Pipe()   // server writes -> client reads
	ctx, cancel := context.WithCancel(context.Background())

	c := &mockClient{t: t, toSrv: clientToServer, fromSrv: bufio.NewReader(clientReads), closer: serverReads}
	c.serveWG.Add(1)
	go func() {
		defer c.serveWG.Done()
		err := srv.ServeStdio(ctx, session, serverReads, serverWrites)
		c.serveMu.Lock()
		c.serveErr = err
		c.serveMu.Unlock()
		_ = serverWrites.Close()
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientToServer.Close()
		c.serveWG.Wait()
	})
	return c, cancel
}

// call sends a request and returns the decoded response. args is marshaled as
// the params.
func (c *mockClient) call(method string, params any) rpcResponse {
	c.t.Helper()
	c.nextID++
	idBytes, _ := json.Marshal(c.nextID)
	req := map[string]any{"jsonrpc": jsonRPCVersion, "id": json.RawMessage(idBytes), "method": method}
	if params != nil {
		pb, err := json.Marshal(params)
		if err != nil {
			c.t.Fatalf("marshal params: %v", err)
		}
		req["params"] = json.RawMessage(pb)
	}
	line, _ := json.Marshal(req)
	if _, err := c.toSrv.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("write request: %v", err)
	}
	respLine, err := c.fromSrv.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read response for %s: %v", method, err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		c.t.Fatalf("decode response: %v (%s)", err, respLine)
	}
	return resp
}

// notify sends a notification (no id, no response expected).
func (c *mockClient) notify(method string) {
	c.t.Helper()
	req := map[string]any{"jsonrpc": jsonRPCVersion, "method": method}
	line, _ := json.Marshal(req)
	if _, err := c.toSrv.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("write notification: %v", err)
	}
}

// initialize performs the MCP handshake and returns the result.
func (c *mockClient) initialize() initializeResult {
	c.t.Helper()
	resp := c.call("initialize", map[string]any{"protocolVersion": protocolVersion})
	if resp.Error != nil {
		c.t.Fatalf("initialize error: %+v", resp.Error)
	}
	var res initializeResult
	remarshal(c.t, resp.Result, &res)
	c.notify("notifications/initialized")
	return res
}

// listTools returns the tool names the session is offered.
func (c *mockClient) listToolNames() []string {
	c.t.Helper()
	resp := c.call("tools/list", nil)
	if resp.Error != nil {
		c.t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var res listToolsResult
	remarshal(c.t, resp.Result, &res)
	names := make([]string, 0, len(res.Tools))
	for _, tdesc := range res.Tools {
		names = append(names, tdesc.Name)
	}
	return names
}

// callTool invokes a tool and returns the tool result plus the JSON-RPC error
// (nil on success).
func (c *mockClient) callTool(name string, args any) (callToolResult, *rpcError) {
	c.t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	resp := c.call("tools/call", params)
	if resp.Error != nil {
		return callToolResult{}, resp.Error
	}
	var res callToolResult
	remarshal(c.t, resp.Result, &res)
	return res, nil
}

// waitServeDone waits up to d for the serve loop to exit and returns its error.
func (c *mockClient) waitServeDone(d time.Duration) (error, bool) {
	done := make(chan struct{})
	go func() { c.serveWG.Wait(); close(done) }()
	select {
	case <-done:
		c.serveMu.Lock()
		defer c.serveMu.Unlock()
		return c.serveErr, true
	case <-time.After(d):
		return nil, false
	}
}

func remarshal(t *testing.T, from any, into any) {
	t.Helper()
	b, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("remarshal decode: %v", err)
	}
}

// --- fakes ------------------------------------------------------------------

type fakeAuth struct {
	tokens  map[string]TokenInfo // raw token -> info
	revoked map[string]bool      // token id -> revoked
	mu      sync.Mutex
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{tokens: map[string]TokenInfo{}, revoked: map[string]bool{}}
}

func (f *fakeAuth) add(raw string, info TokenInfo) { f.tokens[raw] = info }

func (f *fakeAuth) Authenticate(_ context.Context, raw string) (TokenInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.tokens[raw]
	if !ok {
		return TokenInfo{}, errors.New("unknown token")
	}
	if f.revoked[info.ID] {
		return TokenInfo{}, errors.New("revoked")
	}
	return info, nil
}

func (f *fakeAuth) Live(_ context.Context, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.revoked[id]
}

func (f *fakeAuth) revoke(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[id] = true
}
