// SPDX-License-Identifier: Apache-2.0

package mcp

import "encoding/json"

// jsonRPCVersion is the only JSON-RPC version this server speaks. A request
// carrying any other value is rejected with an invalid-request error.
const jsonRPCVersion = "2.0"

// protocolVersion is the MCP protocol revision this server advertises in its
// initialize result. Clients negotiate against it; we echo the client's
// requested version back when it matches, otherwise our own.
const protocolVersion = "2025-06-18"

// serverName / serverVersion identify this implementation in the initialize
// handshake (MCP's serverInfo).
const (
	serverName    = "vnprox"
	serverVersion = "1"
)

// JSON-RPC 2.0 error codes (the reserved range) plus this server's own
// application code for a tool that isn't exposed to the session. An
// out-of-scope tool and a genuinely-unknown tool both return codeUnknownTool
// so scope membership is never leaked by the error alone (the same
// "don't-confirm-existence" stance the tenant/ref-lookup routes take).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	codeUnknownTool    = -32001
)

// rpcRequest is an inbound JSON-RPC 2.0 request or notification. A
// notification has no id (Notification reports that); the server sends no
// response to a notification.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      json.RawMessage `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification reports whether r carried no id and therefore expects no
// response (JSON-RPC 2.0 §4.1).
func (r rpcRequest) Notification() bool { return len(r.ID) == 0 }

// rpcResponse is an outbound JSON-RPC 2.0 response. Exactly one of Result or
// Error is populated.
type rpcResponse struct {
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Data    any    `json:"data,omitempty"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func newResult(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

func newError(id json.RawMessage, code int, message string, data any) *rpcResponse {
	return &rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message, Data: data}}
}

// --- MCP method result shapes ------------------------------------------------

// initializeResult is the MCP `initialize` response: the negotiated protocol
// version, the server's declared capabilities (we implement tools only), and
// its identity.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	// ListChanged is false: this server's tool set is a fixed allowlist that
	// never changes for the life of a session (it is only ever filtered down
	// by the token's scopes at connect time), so there is no list-changed
	// notification to advertise.
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// listToolsResult is the MCP `tools/list` response.
type listToolsResult struct {
	Tools []toolDescriptor `json:"tools"`
}

// toolDescriptor is one tool as advertised to a client (MCP `Tool`): its name,
// human description, and input JSON-schema. RequiredScope is deliberately NOT
// serialized — the client can't influence gating with it; the server derives
// exposure from the token itself.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// callToolResult is the MCP `tools/call` response. Content is the
// model-facing rendering (a single text block carrying the JSON result);
// StructuredContent carries the same payload as a typed object for programmatic
// clients. IsError marks a tool-level failure (as opposed to a protocol-level
// JSON-RPC error).
type callToolResult struct {
	StructuredContent any           `json:"structuredContent,omitempty"`
	Content           []textContent `json:"content"`
	IsError           bool          `json:"isError"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
