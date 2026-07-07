// Package mcpserver is ward-mcp's thin shell over cli-guard's http/opcore
// engine: it parses a `.mcp.kdl` spec into opcore Descriptors, projects each
// grant into one MCP tool, and serves those tools over the MCP wire protocol
// (streamable HTTP + legacy HTTP/SSE). Every tool call is routed straight into
// opcore.Operation.Execute, so the guard (metachar gate, restrict, auth) is the
// engine's, never re-implemented here. cli-mcp is the wire-protocol reference
// only; this package depends on opcore, not on cli-mcp.
package mcpserver

import "encoding/json"

// jsonrpcVersion is the only JSON-RPC version MCP speaks.
const jsonrpcVersion = "2.0"

// request is one inbound JSON-RPC message. A message with no ID is a
// notification and takes no response (id stays a nil RawMessage).
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message carries no id, so it expects no
// reply (JSON-RPC 2.0 notifications, e.g. `notifications/initialized`).
func (r request) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// response is one outbound JSON-RPC message: exactly one of Result or Error is
// set. ID echoes the request's id verbatim.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC error object. MCP reserves protocol-level failures
// (bad method, malformed params) for this; a tool that runs but fails upstream
// reports through the tool result's isError instead. See tools/call.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC error codes MCP uses, from the base spec.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// result builds a success response echoing id.
func result(id json.RawMessage, res any) *response {
	return &response{JSONRPC: jsonrpcVersion, ID: id, Result: res}
}

// errorResponse builds a failure response echoing id.
func errorResponse(id json.RawMessage, code int, msg string) *response {
	return &response{JSONRPC: jsonrpcVersion, ID: id, Error: &rpcError{Code: code, Message: msg}}
}
