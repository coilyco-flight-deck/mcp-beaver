package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// streamAnsweringUpstream answers tools/call over the standalone SSE stream
// rather than on the POST, which is what the playwright build in the fleet
// does. Hand-rolled rather than an SDK server, because every SDK-backed
// fixture answers on the POST and so cannot express this at all - the reason
// mcp-beaver#80 survived three rounds of tests.
func streamAnsweringUpstream(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	sawGET := &atomic.Bool{}
	pending := make(chan string, 4)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			sawGET.Store(true)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flusher", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()
			for {
				select {
				case msg := <-pending:
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
					flusher.Flush()
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Second):
					return
				}
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &msg)

		switch {
		case msg.Method == "server/discover":
			// Not implemented here, the same as the Node upstreams, so the
			// client falls back to a plain initialize.
			http.Error(w, "not found", http.StatusNotFound)
		case msg.Method == "initialize":
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			writeJSON(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{
				"protocolVersion":"2025-06-18","capabilities":{"tools":{}},
				"serverInfo":{"name":"fixture","version":"0.1.0"}}}`, msg.ID))
		case msg.Method == "tools/list":
			writeJSON(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"tools":[
				{"name":"browse","description":"browse upstream","inputSchema":
				{"type":"object","properties":{"q":{"type":"string"}}}}]}}`, msg.ID))
		case msg.Method == "tools/call":
			// The answer goes to the standalone stream and this response is
			// held open with nothing on it, so a client holding no stream
			// waits for an answer it can never see.
			// One line, because an SSE data field cannot carry a newline.
			pending <- fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"upstream answered on the stream"}]}}`,
				msg.ID)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no flusher", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()
			// Bounded so the fixture cannot wedge the server's shutdown. A
			// real upstream holds this open for the session's life.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	return httptest.NewServer(mux), sawGET
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, bytes.NewReader([]byte(body)))
}

// The whole fleet's browser was dead for days on this: tools/list answers on
// the POST and worked, so the startup snapshot and the drift check both passed
// and the surface looked healthy, while every real tool call hung until the
// request budget expired. See mcp-beaver#80 and sirens-echo#897.
func TestUpstreamToolCallCompletesWhenAnsweredOnTheStream(t *testing.T) {
	upstreamTS, sawGET := streamAnsweringUpstream(t)
	defer upstreamTS.Close()

	s, err := NewProxy(context.Background(), "proxy", "", upstreamTS.URL+"/mcp",
		[]string{"browse"}, upstreamTS.Client())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	if out := decodeRPCResponse(t, initResp); out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}

	done := make(chan map[string]any, 1)
	go func() {
		resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browse","arguments":{"q":"ramen"}}}`)
		var result map[string]any
		if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
			t.Errorf("call result: %v", err)
		}
		done <- result
	}()

	select {
	case result := <-done:
		if isErr, _ := result["isError"].(bool); isErr {
			t.Fatalf("call reported isError; content=%v", result["content"])
		}
		if got := firstText(t, result); !strings.Contains(got, "answered on the stream") {
			t.Fatalf("call result = %q, want the upstream's stream answer", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("tool call never completed: the client is holding no standalone " +
			"SSE stream, so an upstream that answers there is never heard")
	}

	if !sawGET.Load() {
		t.Error("no standalone SSE stream was opened, so a stream-answering " +
			"upstream cannot reach this client")
	}
}
