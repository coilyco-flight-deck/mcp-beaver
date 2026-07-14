package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roundTripSpec points its base-url at a mock upstream (set per-test) and
// grants one read and one write tool plus an owner restriction, so a single spec
// covers projection, path/query/body routing, and the guard.
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

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// newTestHandler builds a handler over a two-tool spec pointed at a dead
// upstream. The transport tests exercise initialize, tools/list, and tools/call
// through the SDK-backed /mcp endpoint.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	s, err := New("test", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s.Handler()
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(newTestHandler(t))
}

// postJSON posts one JSON-RPC message to path and returns the raw response.
func postToServer(t *testing.T, client *http.Client, url, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-03-26")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodeRPCResponse(t *testing.T, resp *http.Response) rpcResponse {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		raw = sseData(t, raw, "message")
	}
	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode rpc response: %v\nbody: %s", err, raw)
	}
	return out
}

func sseData(t *testing.T, raw []byte, wantEvent string) []byte {
	t.Helper()
	lines := bytes.Split(raw, []byte("\n"))
	var event string
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			event = string(bytes.TrimPrefix(line, []byte("event: ")))
		case bytes.HasPrefix(line, []byte("data: ")):
			if event == wantEvent {
				return bytes.TrimPrefix(line, []byte("data: "))
			}
		}
	}
	t.Fatalf("no %q event in SSE body: %s", wantEvent, raw)
	return nil
}

func toolList(t *testing.T, resp rpcResponse) []map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	rawTools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools field is %T, want []any", result["tools"])
	}
	out := make([]map[string]any, 0, len(rawTools))
	for _, item := range rawTools {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tool item is %T, want map", item)
		}
		out = append(out, m)
	}
	return out
}

// TestInitializeHandshake proves the SDK-backed initialize call negotiates the
// protocol, advertises tools, and issues a session id header.
func TestInitializeHandshake(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Mcp-Session-Id"); got == "" {
		t.Fatal("initialize response missing Mcp-Session-Id")
	}
	out := decodeRPCResponse(t, resp)
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want 2025-03-26", result["protocolVersion"])
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing tools; got %v", caps)
	}
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "test" {
		t.Errorf("serverInfo.name = %v, want test", serverInfo["name"])
	}
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	out := decodeRPCResponse(t, initResp)
	if out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := toolList(t, decodeRPCResponse(t, resp))
	got := map[string]bool{}
	for _, tl := range tools {
		if name, _ := tl["name"].(string); name != "" {
			got[name] = true
		}
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
	create := findTool(t, tools, "create_issue")
	var schema map[string]any
	if err := toJSON(create["inputSchema"], &schema); err != nil {
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-flight-deck","id":"42","verbose":"true"}}}`)
	out := decodeRPCResponse(t, resp)
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("tool call reported isError; content=%v", result["content"])
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
	text := firstText(t, result)
	if !strings.Contains(text, `"name":"widget"`) {
		t.Errorf("tool content = %q, want the upstream body", text)
	}
}

// TestToolCallBodyRoundTrip proves a write tool sends its body fields as JSON
// and path params still route, distinct from the GET path.
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_thing","arguments":{"owner":"coilyco-x","title":"hello"}}}`)
	out := decodeRPCResponse(t, resp)
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("create call reported isError; content=%v", result["content"])
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotBody["title"] != "hello" {
		t.Errorf("upstream body = %v, want title=hello", gotBody)
	}
}

// TestToolCallRestrictDenied proves the guard is opcore's, not re-implemented:
// a path value outside the `restrict owner matches coilyco-*` allowlist comes
// back as a tool error and never reaches the upstream.
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"someone-else","id":"1"}}}`)
	out := decodeRPCResponse(t, resp)
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("restrict-denied call should be an isError result; got %v", result)
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
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_thing","arguments":{}}}`)
	out := decodeRPCResponse(t, resp)
	if out.Error == nil {
		t.Fatal("calling an ungranted tool should be a JSON-RPC error")
	}
	if out.Error.Code != -32601 {
		t.Errorf("error code = %d, want method-not-found -32601", out.Error.Code)
	}
}

// TestHealth proves the runtime still exposes the pod liveness probe.
func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("health body = %v, want status ok", body)
	}
}

func findTool(t *testing.T, tools []map[string]any, name string) map[string]any {
	t.Helper()
	for _, tl := range tools {
		if tl["name"] == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return nil
}

func firstText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content is %T / empty: %v", res["content"], res["content"])
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func toJSON(v any, out any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
