package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func postAPI(t *testing.T, client *http.Client, url, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new API request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodeAPIResult(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode API result: %v", err)
	}
	return out
}

func TestAPILocalToolRoundTrip(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "api-test-token")
	var gotPath, gotQuery, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_thing", "application/json; charset=utf-8", `{"owner":"coilyco-flight-deck","id":"42","search_query":"platform"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	body := decodeAPIResult(t, resp)
	structured, _ := body["structuredContent"].(map[string]any)
	result, _ := structured["result"].(map[string]any)
	if result["ok"] != true {
		t.Fatalf("structuredContent = %v, want result.ok=true", structured)
	}
	if gotPath != "/owners/coilyco-flight-deck/things/42" || gotQuery != "platform" {
		t.Fatalf("upstream request = %s?query=%s", gotPath, gotQuery)
	}
	if gotAuth != "Bearer api-test-token" {
		t.Fatalf("upstream Authorization = %q, want injected bearer token", gotAuth)
	}
}

func TestAPIFailsClosedAtHTTPBoundary(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		want        int
	}{
		{name: "method", method: http.MethodGet, path: "/api/get_thing", want: http.StatusMethodNotAllowed},
		{name: "unknown", method: http.MethodPost, path: "/api/delete_thing", contentType: "application/json", body: `{}`, want: http.StatusNotFound},
		{name: "nested path", method: http.MethodPost, path: "/api/get_thing/extra", contentType: "application/json", body: `{}`, want: http.StatusNotFound},
		{name: "missing content type", method: http.MethodPost, path: "/api/get_thing", body: `{}`, want: http.StatusUnsupportedMediaType},
		{name: "wrong content type", method: http.MethodPost, path: "/api/get_thing", contentType: "text/plain", body: `{}`, want: http.StatusUnsupportedMediaType},
		{name: "malformed", method: http.MethodPost, path: "/api/get_thing", contentType: "application/json", body: `{`, want: http.StatusBadRequest},
		{name: "array", method: http.MethodPost, path: "/api/get_thing", contentType: "application/json", body: `[]`, want: http.StatusBadRequest},
		{name: "null", method: http.MethodPost, path: "/api/get_thing", contentType: "application/json", body: `null`, want: http.StatusBadRequest},
		{name: "trailing value", method: http.MethodPost, path: "/api/get_thing", contentType: "application/json", body: `{} {}`, want: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), tt.method, ts.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body := decodeAPIResult(t, resp)
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d; body = %v", resp.StatusCode, tt.want, body)
			}
			if body["error"] == "" {
				t.Fatalf("error body = %v", body)
			}
		})
	}

	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_thing", "application/json", `{"owner":"someone-else","id":"1"}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("guard-denied status = %d, want 502; body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	result := decodeAPIResult(t, resp)
	if result["isError"] != true {
		t.Fatalf("guard-denied body = %v, want isError=true", result)
	}
}

func TestAPIRejectsOversizedBody(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	body := `{"value":"` + strings.Repeat("x", maxAPIBodyBytes) + `"}`
	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_thing", "application/json", body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	result := decodeAPIResult(t, resp)
	if result["error"] != "request body exceeds 1 MiB" {
		t.Fatalf("body = %v", result)
	}
}

func TestAPIUpstreamProxyRoundTrip(t *testing.T) {
	upstreamServer := upstreamTool(t, "browse", "browse upstream", `{
		"type":"object",
		"properties":{"q":{"type":"string"}},
		"required":["q"]
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "upstream:" + args["q"].(string)}}}, nil, nil
	})
	upstream := newUpstreamServer(t, upstreamServer)
	defer upstream.Close()

	s, err := NewProxy(context.Background(), "proxy", "", upstream.URL+"/mcp", []string{"browse"}, upstream.Client())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	defer func() { _ = s.Close() }()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json", `{"q":"ramen"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	body := decodeAPIResult(t, resp)
	content, _ := body["content"].([]any)
	first, _ := content[0].(map[string]any)
	if first["text"] != "upstream:ramen" {
		t.Fatalf("content = %v", content)
	}
}

func TestAPISSMRoundTrip(t *testing.T) {
	client := &fakeSSM{}
	s, err := newSSMServer("ssm", "ssm.mcp.kdl", ssmPolicy{
		Region:    "us-east-1",
		Parameter: "/allowed",
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_parameter", "application/json", `{"name":"/allowed"}`)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, raw)
	}
	body := decodeAPIResult(t, resp)
	structured, _ := body["structuredContent"].(map[string]any)
	if structured["name"] != "/allowed" || client.input == nil {
		t.Fatalf("structuredContent = %v, input = %#v", structured, client.input)
	}
}
