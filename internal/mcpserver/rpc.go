package mcpserver

// rpcError mirrors the JSON-RPC error envelope the tests assert on. The
// transport itself is now the SDK's job.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
