package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestProxyRefusesUndeclaredArgument is mcp-beaver#94's negative control on the
// proxy path, the shape signoz-mcp runs. A filter argument the upstream tool
// does not declare must end the call, because the alternative is the upstream
// ignoring it and answering the unfiltered question as if it were the asked one.
func TestProxyRefusesUndeclaredArgument(t *testing.T) {
	var sawSearchText atomic.Bool
	upstream := upstreamTool(t, "aggregate_logs", "count logs", `{
		"type":"object",
		"properties":{"aggregation":{"type":"string"}},
		"required":["aggregation"]
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		if _, ok := args["searchText"]; ok {
			sawSearchText.Store(true)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "8759997"}}}, nil, nil
	})
	upstreamTS, _ := singleSessionUpstream(t, upstream)
	defer upstreamTS.Close()

	s, err := NewProxy(context.Background(), "proxy", "", upstreamTS.URL+"/mcp", []string{"aggregate_logs"}, upstreamTS.Client())
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

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"aggregate_logs","arguments":{"aggregation":"count","searchText":"zzzzz-nonexistent-qqqq"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("an undeclared filter argument must be refused, got: %v", result)
	}
	if got := firstText(t, result); !strings.Contains(got, "searchText") {
		t.Errorf("the refusal must name the argument it refused, got %q", got)
	}
	if sawSearchText.Load() {
		t.Error("a refused call must not reach the upstream")
	}

	// The control: the declared surface still works unchanged.
	ok := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"aggregate_logs","arguments":{"aggregation":"count"}}}`)
	var okResult map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, ok).Result, &okResult); err != nil {
		t.Fatalf("control result: %v", err)
	}
	if isErr, _ := okResult["isError"].(bool); isErr {
		t.Fatalf("the declared surface must still serve: %v", okResult)
	}
}

// TestDeclaredPropertiesHonoursAnOpenSchema keeps the refusal off an upstream
// that says in its own schema that it takes more than it lists. A guard is
// stricter than the thing it guards, not stricter than what that thing declared.
func TestDeclaredPropertiesHonoursAnOpenSchema(t *testing.T) {
	cases := map[string]struct {
		schema string
		closed bool
	}{
		"absent additionalProperties is closed": {`{"type":"object","properties":{"a":{"type":"string"}}}`, true},
		"additionalProperties false is closed":  {`{"type":"object","properties":{"a":{}},"additionalProperties":false}`, true},
		"additionalProperties true is open":     {`{"type":"object","properties":{"a":{}},"additionalProperties":true}`, false},
		"additionalProperties schema is open":   {`{"type":"object","properties":{"a":{}},"additionalProperties":{"type":"string"}}`, false},
		"no properties at all is open":          {`{"type":"object"}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, closed := declaredProperties(json.RawMessage(tc.schema))
			if closed != tc.closed {
				t.Errorf("closed = %v, want %v", closed, tc.closed)
			}
		})
	}
}
