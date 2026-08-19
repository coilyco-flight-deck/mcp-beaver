package mcpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// versionBoundUpstream refuses any MCP-Protocol-Version it does not know, which
// is what every Node upstream in the fleet does and what the playwright build
// answering sirens-echo#897 does verbatim.
func versionBoundUpstream(t *testing.T, server *mcp.Server, supported ...string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	seen := &atomic.Value{}
	seen.Store("")
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		got := r.Header.Get("MCP-Protocol-Version")
		// server/discover is the SDK's own probe on its newest version and is
		// meant to be refused, falling back to a plain initialize. Only the
		// working methods are evidence of what the session actually carries.
		if bytes.Contains(body, []byte(`"method":"tools/`)) {
			seen.Store(got)
		}
		if got != "" && !slices.Contains(supported, got) {
			http.Error(w, "Bad Request: Unsupported protocol version: "+got,
				http.StatusBadRequest)
			return
		}
		inner.ServeHTTP(w, r)
	})
	return httptest.NewServer(mux), seen
}

// The caller's negotiated protocol version must not reach the upstream. The
// SDK's server puts it in the request context and its client prefers that over
// the session's own, so a caller newer than the upstream made every upstream
// request carry a version it rejects: 14 of 14 browser calls on sirens-dowel
// failed this way while a probe on an older version passed. See #85.
func TestUpstreamCallDoesNotCarryTheCallersProtocolVersion(t *testing.T) {
	upstream := upstreamTool(t, "browse", "browse upstream", `{
		"type":"object",
		"properties":{"q":{"type":"string"}},
		"required":["q"]
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "upstream:" + args["q"].(string)}}}, nil, nil
	})
	// Deliberately narrower than the SDK's newest, so a leaked version fails.
	upstreamTS, seen := versionBoundUpstream(t, upstream, "2025-06-18", "2025-11-25")
	defer upstreamTS.Close()

	s, err := NewProxy(context.Background(), "proxy", "", upstreamTS.URL+"/mcp",
		[]string{"browse"}, upstreamTS.Client())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// A caller on the newest version the SDK speaks, which is what the harness is.
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.1.0"}, nil)
	session, err := client.Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("caller connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse", Arguments: map[string]any{"q": "ramen"},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	carried := seen.Load().(string)
	if carried == "" {
		t.Fatal("no tools request reached the upstream, so this proves nothing")
	}
	if caller := session.InitializeResult().ProtocolVersion; carried == caller {
		t.Errorf("upstream tools requests carried the caller's protocol %q; they must "+
			"carry the version mcp-beaver negotiated with the upstream", caller)
	}
	if res.IsError {
		text := ""
		for _, content := range res.Content {
			if block, ok := content.(*mcp.TextContent); ok {
				text += block.Text
			}
		}
		t.Fatalf("tool call refused: %s", text)
	}
	if !strings.Contains(firstTextOf(t, res), "upstream:ramen") {
		t.Errorf("result = %q, want upstream:ramen", firstTextOf(t, res))
	}
}

func firstTextOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range res.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			return block.Text
		}
	}
	return ""
}
