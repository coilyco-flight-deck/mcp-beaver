package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roundTripSpec points its base-url at a mock upstream (set per-test) and grants
// one read and one write tool plus an owner restriction, so a single spec covers
// projection, path/query/body routing, and the guard.
func roundTripSpec(baseURL string) string {
	return `wrap ward mcp test {
    base-url "` + baseURL + `"
    auth bearer { value env "WARD_MCP_TEST_TOKEN" }
    restrict owner matches "coilyco-*"
    can get thing {
        path "/owners/{owner}/things/{id}"
        query "verbose"
    }
    can create thing {
        path "/owners/{owner}/things"
        body "title"
    }
}`
}

// dispatchJSON marshals params, dispatches one request through the server, and
// returns the decoded result map, failing the test on any JSON-RPC error.
func dispatchJSON(t *testing.T, s *Server, method string, params any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := s.dispatch(context.Background(), request{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  raw,
	})
	if resp == nil {
		t.Fatalf("%s: got nil response, want a result", method)
	}
	if resp.Error != nil {
		t.Fatalf("%s: JSON-RPC error: %+v", method, resp.Error)
	}
	out, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("%s: result is %T, want map", method, resp.Result)
	}
	return out
}

// TestToolsListFromSpec proves the tool list is derived from a `.mcp.kdl`: the
// committed forgejo example projects exactly its five grants, one tool each,
// named verb_resource, with no delete tool (deny-by-absence).
func TestToolsListFromSpec(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "examples", "forgejo-issues.mcp.kdl"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	s, err := New("forgejo-issues", src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := dispatchJSON(t, s, "tools/list", map[string]any{})
	tools, ok := res["tools"].([]tool)
	if !ok {
		t.Fatalf("tools field is %T, want []tool", res["tools"])
	}
	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.Name] = true
	}
	want := []string{"create_issue", "get_issue", "list_issue", "comment_issue", "close_issue"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tool %q; got %v", w, got)
		}
	}
	if got["delete_issue"] {
		t.Error("delete_issue was minted; deny-by-absence should have withheld it")
	}
	if len(tools) != len(want) {
		t.Errorf("tool count = %d, want %d (%v)", len(tools), len(want), got)
	}

	// The derived inputSchema must be valid draft-07 with the grant's fields.
	create := findTool(t, tools, "create_issue")
	var schema map[string]any
	if err := json.Unmarshal(create.InputSchema, &schema); err != nil {
		t.Fatalf("create_issue schema not JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"owner", "repo", "title", "body"} {
		if _, ok := props[field]; !ok {
			t.Errorf("create_issue schema missing property %q; got %v", field, props)
		}
	}
}

// TestToolCallRoundTrip drives one tool call end to end against a mock upstream:
// arguments split onto path/query/body, the token is signed in, and the decoded
// response renders back as MCP tool content with isError false.
func TestToolCallRoundTrip(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "s3cr3t")

	var gotMethod, gotPath, gotQuery, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"name":"widget"}`))
	}))
	defer upstream.Close()

	s, err := New("test", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res := dispatchJSON(t, s, "tools/call", toolCallParams{
		Name: "get_thing",
		Arguments: map[string]any{
			"owner":   "coilyco-flight-deck",
			"id":      "42",
			"verbose": "true",
		},
	})

	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("tool call reported isError; content=%v", res["content"])
	}
	if gotMethod != http.MethodGet {
		t.Errorf("upstream method = %q, want GET", gotMethod)
	}
	if gotPath != "/owners/coilyco-flight-deck/things/42" {
		t.Errorf("upstream path = %q", gotPath)
	}
	if gotQuery != "verbose=true" {
		t.Errorf("upstream query = %q, want verbose=true", gotQuery)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("upstream auth = %q, want Bearer s3cr3t", gotAuth)
	}

	// The mock body is rendered back verbatim as text content.
	text := firstText(t, res)
	if !strings.Contains(text, `"name":"widget"`) {
		t.Errorf("tool content = %q, want the upstream body", text)
	}
}

// TestToolCallBodyRoundTrip proves a write tool sends its body fields as JSON and
// path params still route, distinct from the GET path.
func TestToolCallBodyRoundTrip(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "s3cr3t")

	var gotBody map[string]any
	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s, err := New("test", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := dispatchJSON(t, s, "tools/call", toolCallParams{
		Name:      "create_thing",
		Arguments: map[string]any{"owner": "coilyco-x", "title": "hello"},
	})
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("create call reported isError; content=%v", res["content"])
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotBody["title"] != "hello" {
		t.Errorf("upstream body = %v, want title=hello", gotBody)
	}
}

// TestToolCallRestrictDenied proves the guard is opcore's, not re-implemented: a
// path value outside the `restrict owner matches coilyco-*` allowlist comes back
// as a tool error and never reaches the upstream.
func TestToolCallRestrictDenied(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "s3cr3t")

	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	s, err := New("test", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := dispatchJSON(t, s, "tools/call", toolCallParams{
		Name:      "get_thing",
		Arguments: map[string]any{"owner": "someone-else", "id": "1"},
	})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("restrict-denied call should be an isError result; got %v", res)
	}
	if hit {
		t.Error("upstream was called despite a restrict denial")
	}
}

// TestUnknownToolDenied proves deny-by-absence at call time: a tool the spec
// never granted is a method-not-found, not a silent pass.
func TestUnknownToolDenied(t *testing.T) {
	s, err := New("test", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := s.dispatch(context.Background(), request{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"delete_thing","arguments":{}}`),
	})
	if resp.Error == nil {
		t.Fatal("calling an ungranted tool should be a JSON-RPC error")
	}
	if resp.Error.Code != codeMethodNotFound {
		t.Errorf("error code = %d, want method-not-found %d", resp.Error.Code, codeMethodNotFound)
	}
}

// TestInitializeHandshake proves the MCP handshake echoes a known protocol
// version and advertises the tools capability.
func TestInitializeHandshake(t *testing.T) {
	s, err := New("test", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := dispatchJSON(t, s, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want the echoed 2025-03-26", res["protocolVersion"])
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing tools; got %v", caps)
	}
}

func findTool(t *testing.T, tools []tool, name string) tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return tool{}
}

// firstText pulls the first text block out of an MCP tool result's content.
func firstText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, ok := res["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content is %T / empty: %v", res["content"], res["content"])
	}
	text, _ := content[0]["text"].(string)
	return text
}
