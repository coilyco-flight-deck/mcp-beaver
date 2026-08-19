package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// singleSessionUpstream serves one MCP handshake and rejects every later one
// with HTTP 400, which is how the Node upstreams in the fleet behave. Fronting
// mcp-beaver with mcp-beaver hid mcp-beaver#67 precisely because the Go SDK
// accepts a second session, so the fixture has to enforce the constraint the
// real upstreams do rather than borrow another Go server.
func singleSessionUpstream(t *testing.T, server *mcp.Server) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	handshakes := &atomic.Int64{}
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Answered on the POST, so no standalone stream is offered. Holding
		// one open would outlive the test, since httptest waits for
		// outstanding requests before it closes.
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if bytes.Contains(body, []byte(`"method":"initialize"`)) && handshakes.Add(1) > 1 {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		inner.ServeHTTP(w, r)
	})
	return httptest.NewServer(mux), handshakes
}

// A tool call must not open a second upstream session. Every serve-upstream
// deployment in the fleet was non-functional at call time because the drift
// check dialled one, and the upstream answered `notifications/initialized`
// with 400.
func TestUpstreamProxyReusesOneSessionPerCall(t *testing.T) {
	upstream := upstreamTool(t, "browse", "browse upstream", `{
		"type":"object",
		"properties":{"q":{"type":"string"}},
		"required":["q"]
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "upstream:" + args["q"].(string)}}}, nil, nil
	})
	upstreamTS, handshakes := singleSessionUpstream(t, upstream)
	defer upstreamTS.Close()

	s, err := NewProxy(context.Background(), "proxy", "", upstreamTS.URL+"/mcp", []string{"browse"}, upstreamTS.Client())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	if out := decodeRPCResponse(t, initResp); out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}

	// Twice, because the first call is what regressed and a per-call dial
	// would only start accumulating from there.
	for _, id := range []string{"2", "3"} {
		resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID,
			`{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":"browse","arguments":{"q":"ramen"}}}`)
		var result map[string]any
		if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
			t.Fatalf("call %s result: %v", id, err)
		}
		if isErr, _ := result["isError"].(bool); isErr {
			t.Fatalf("call %s reported isError; content=%v", id, result["content"])
		}
		if got := firstText(t, result); !strings.Contains(got, "upstream:ramen") {
			t.Fatalf("call %s result = %q, want upstream:ramen", id, got)
		}
	}

	if got := handshakes.Load(); got != 1 {
		t.Fatalf("upstream handshakes = %d, want 1 (the startup session, reused for every call)", got)
	}
}
