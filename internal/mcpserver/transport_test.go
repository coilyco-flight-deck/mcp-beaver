package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestHandler builds a handler over a two-tool spec pointed at a dead
// upstream (transport tests never fire a call, only list/handshake).
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	s, err := New("test", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s.Handler()
}

// postJSON posts one JSON-RPC message to path and returns the raw response.
func postJSON(t *testing.T, h http.Handler, path, accept, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// TestStreamableHTTPJSON proves the /mcp streamable transport answers tools/list
// as a plain JSON response when the client does not ask for an event stream.
func TestStreamableHTTPJSON(t *testing.T) {
	h := newTestHandler(t)
	resp := postJSON(t, h, "/mcp", "application/json", `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out.ID) != "7" {
		t.Errorf("id = %s, want 7", out.ID)
	}
	res, _ := out.Result.(map[string]any)
	if _, ok := res["tools"]; !ok {
		t.Errorf("result missing tools: %v", out.Result)
	}
}

// TestStreamableHTTPSSE proves the same endpoint frames its reply as one SSE
// `message` event when the client accepts text/event-stream.
func TestStreamableHTTPSSE(t *testing.T) {
	h := newTestHandler(t)
	resp := postJSON(t, h, "/mcp", "text/event-stream", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	body := buf.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"tools"`) {
		t.Errorf("SSE frame missing message/tools: %q", body)
	}
}

// TestStreamableNotificationAccepted proves a notification (no id) gets a 202
// with no body, per JSON-RPC semantics.
func TestStreamableNotificationAccepted(t *testing.T) {
	h := newTestHandler(t)
	resp := postJSON(t, h, "/mcp", "application/json", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202 for a notification", resp.StatusCode)
	}
}

// TestLegacySSEHandshake drives the 2024-11-05 transport: GET /sse announces the
// POST-back endpoint, and a POST to it pushes the reply back over the stream.
func TestLegacySSEHandshake(t *testing.T) {
	s, err := New("test", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Open the SSE stream and read the first `endpoint` event.
	sseResp, err := http.Get(ts.URL + "/sse") //nolint:noctx // test client
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	reader := bufio.NewReader(sseResp.Body)

	endpoint := readSSEData(t, reader, "endpoint")
	if !strings.HasPrefix(endpoint, "/messages?sessionId=") {
		t.Fatalf("endpoint event = %q, want /messages?sessionId=...", endpoint)
	}

	// POST a tools/list to the announced endpoint; the reply arrives over /sse.
	postResp, err := http.Post(ts.URL+endpoint, "application/json", //nolint:noctx // test client
		strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("POST /messages: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", postResp.StatusCode)
	}

	msg := readSSEData(t, reader, "message")
	if !strings.Contains(msg, `"tools"`) {
		t.Errorf("pushed message missing tools: %q", msg)
	}
}

// readSSEData reads SSE frames until it sees the named event, returning its data
// line. It fails the test if the stream ends first.
func readSSEData(t *testing.T, r *bufio.Reader, wantEvent string) string {
	t.Helper()
	var event, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("SSE stream ended before %q event", wantEvent)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if event == wantEvent {
				return data
			}
			event, data = "", ""
		}
	}
}
