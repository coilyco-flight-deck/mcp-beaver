package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func typedQuerySpec(baseURL string) string {
	return `wrap ward mcp typed-query-test {
    base-url "` + baseURL + `"
    auth bearer { value env "WARD_MCP_TYPED_QUERY_TEST_TOKEN" }
    can search thing {
        path "/things"
        query {
            field "content" type="string"
            field "limit" type="integer" minimum=1 maximum=100 required=#true
            field "pinned" type="boolean"
            field "ratio" type="number" minimum=0 maximum=1
            array "author_id" items="string" min-items=1 max-items=3
            field "before" type="string"
            field "after" type="string"
            field "around" type="string"
            mutually-exclusive "before" "after" "around"
        }
    }
}`
}

func typedQuerySession(t *testing.T, upstreamURL string) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("WARD_MCP_TYPED_QUERY_TEST_TOKEN", "test-token")
	s, err := New("typed-query-test", "typed-query-test.mcp.kdl", []byte(typedQuerySpec(upstreamURL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ward-mcp-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	if out := decodeRPCResponse(t, initResp); out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}
	return ts, sessionID
}

func TestTypedQueryToolsListSchema(t *testing.T) {
	ts, sessionID := typedQuerySession(t, "http://127.0.0.1:1")
	listResp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := toolList(t, decodeRPCResponse(t, listResp))
	search := findTool(t, tools, "search_thing")

	schema, ok := search["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema is %T, want object", search["inputSchema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want object", schema["properties"])
	}

	assertSchemaProperty(t, props, "content", "string")
	limit := assertSchemaProperty(t, props, "limit", "integer")
	if limit["minimum"] != float64(1) || limit["maximum"] != float64(100) {
		t.Fatalf("limit bounds = [%v,%v], want [1,100]", limit["minimum"], limit["maximum"])
	}
	assertSchemaProperty(t, props, "pinned", "boolean")
	ratio := assertSchemaProperty(t, props, "ratio", "number")
	if ratio["minimum"] != float64(0) || ratio["maximum"] != float64(1) {
		t.Fatalf("ratio bounds = [%v,%v], want [0,1]", ratio["minimum"], ratio["maximum"])
	}
	authors := assertSchemaProperty(t, props, "author_id", "array")
	items, ok := authors["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Fatalf("author_id items = %v, want string schema", authors["items"])
	}
	if authors["minItems"] != float64(1) || authors["maxItems"] != float64(3) {
		t.Fatalf("author_id bounds = [%v,%v], want [1,3]", authors["minItems"], authors["maxItems"])
	}

	required, ok := schema["required"].([]any)
	if !ok || !reflect.DeepEqual(required, []any{"limit"}) {
		t.Fatalf("required = %v, want [limit]", schema["required"])
	}
	assertMutualExclusion(t, schema, [][2]string{
		{"before", "after"},
		{"before", "around"},
		{"after", "around"},
	})
}

func TestTypedQueryToolCallPreservesTypesAndRepeatedOrder(t *testing.T) {
	var gotQuery map[string][]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	ts, sessionID := typedQuerySession(t, upstream.URL)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_thing","arguments":{"content":"platform","limit":25,"pinned":true,"ratio":0.5,"author_id":["second","first","third"],"before":"123"}}}`
	out := decodeRPCResponse(t, postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, call))
	if out.Error != nil {
		t.Fatalf("tools/call error: %+v", out.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("tool call reported isError: %v", result)
	}

	want := map[string][]string{
		"content":   {"platform"},
		"limit":     {"25"},
		"pinned":    {"true"},
		"ratio":     {"0.5"},
		"author_id": {"second", "first", "third"},
		"before":    {"123"},
	}
	if !reflect.DeepEqual(gotQuery, want) {
		t.Fatalf("upstream query = %#v, want %#v", gotQuery, want)
	}
}

func TestTypedQueryToolCallRejectsInvalidInputsBeforeUpstream(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		_, _ = w.Write([]byte(`{"unexpected":true}`))
	}))
	defer upstream.Close()

	tests := map[string]string{
		"wrong scalar type":  `{"limit":"25"}`,
		"below minimum":      `{"limit":0}`,
		"above maximum":      `{"limit":101}`,
		"below min items":    `{"limit":25,"author_id":[]}`,
		"above max items":    `{"limit":25,"author_id":["a","b","c","d"]}`,
		"mutually exclusive": `{"limit":25,"before":"1","after":"2"}`,
	}
	for name, arguments := range tests {
		t.Run(name, func(t *testing.T) {
			ts, sessionID := typedQuerySession(t, upstream.URL)
			call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_thing","arguments":` + arguments + `}}`
			out := decodeRPCResponse(t, postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, call))
			assertRejectedToolCall(t, out)
		})
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("invalid calls reached upstream %d times, want 0", got)
	}
}

func assertSchemaProperty(t *testing.T, props map[string]any, name, wantType string) map[string]any {
	t.Helper()
	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("property %q = %T, want object", name, props[name])
	}
	if prop["type"] != wantType {
		t.Fatalf("property %q type = %v, want %s", name, prop["type"], wantType)
	}
	return prop
}

func assertMutualExclusion(t *testing.T, schema map[string]any, wantPairs [][2]string) {
	t.Helper()
	allOf, ok := schema["allOf"].([]any)
	if !ok {
		t.Fatalf("allOf is %T, want array", schema["allOf"])
	}
	got := map[[2]string]bool{}
	for _, raw := range allOf {
		clause, _ := raw.(map[string]any)
		notClause, _ := clause["not"].(map[string]any)
		required, _ := notClause["required"].([]any)
		if len(required) != 2 {
			t.Fatalf("mutual-exclusion clause = %v, want two required names", raw)
		}
		got[[2]string{required[0].(string), required[1].(string)}] = true
	}
	for _, pair := range wantPairs {
		if !got[pair] {
			t.Fatalf("allOf omitted mutually-exclusive pair %v: %v", pair, allOf)
		}
	}
	if len(got) != len(wantPairs) {
		t.Fatalf("mutual-exclusion pair count = %d, want %d: %v", len(got), len(wantPairs), allOf)
	}
}

func assertRejectedToolCall(t *testing.T, out rpcResponse) {
	t.Helper()
	if out.Error != nil {
		return
	}
	var result map[string]any
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("invalid tool call succeeded: %v", result)
	}
}
