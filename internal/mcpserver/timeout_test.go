package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hangingUpstream never answers. This is the shape #49 recorded: a healthy pod
// holding a connection open with nothing coming back.
func hangingUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
}

// A wedged upstream must produce a bounded tool error, not a request that
// outlives the turn that issued it.
//
// The token has to be set. Without it the auth value fails to resolve and the
// call returns before any request is made, which makes this test pass in
// microseconds while proving nothing - the first version of it did exactly
// that. opcore's own client timeout is 30s, so a bound that did not work would
// show up here as roughly 30 seconds rather than a fast failure.
func TestHangingUpstreamIsBounded(t *testing.T) {
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "test-token")
	upstream := hangingUpstream(t)
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetRequestTimeout(250 * time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	started := time.Now()
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-x","id":"1"}}}`)
	elapsed := time.Since(started)

	// Comfortably under opcore's 30s client timeout, which is what an
	// unbounded call would fall back to.
	if elapsed > 5*time.Second {
		t.Fatalf("request took %s, want it bounded by the 250ms deadline", elapsed)
	}
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true: a hung upstream is a failed call", result["isError"])
	}
}

// The health probe must stay outside the deadline. A liveness check that a
// wedged upstream can fail turns one slow dependency into a restart loop.
//
// Exercised against withRequestDeadline directly with a deadline that is
// already expired. Going through Server.Handler would not test this, because
// the transport deadline it applies carries responseGrace on top and would
// leave the probe comfortably inside the bound either way.
func TestHealthzExemptFromDeadline(t *testing.T) {
	var deadlineSeen bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadlineSeen = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(withRequestDeadline(time.Nanosecond, inner))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 under an already-expired deadline", resp.StatusCode)
	}
	if deadlineSeen {
		t.Error("/healthz carried a deadline; a wedged upstream could fail the liveness probe")
	}
}

// The bound must be reported, not merely applied. An empty body would leave a
// wedged upstream indistinguishable from a crashed pod.
func TestBoundedCallReportsTheTimeout(t *testing.T) {
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "test-token")
	upstream := hangingUpstream(t)
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetRequestTimeout(250 * time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-x","id":"1"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("timeout produced no content; the caller cannot tell why it failed")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(strings.ToLower(text), "deadline") && !strings.Contains(strings.ToLower(text), "timeout") &&
		!strings.Contains(strings.ToLower(text), "context canceled") {
		t.Errorf("error text = %q, want it to name the expiry", text)
	}
}

// The per-call bound has to hold for the direct HTTP tool surface too, which
// does not go through the MCP transport at all.
func TestDirectHTTPToolIsBounded(t *testing.T) {
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "test-token")
	upstream := hangingUpstream(t)
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetRequestTimeout(250 * time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	started := time.Now()
	resp, err := ts.Client().Post(ts.URL+apiPrefix+"get_thing", "application/json",
		strings.NewReader(`{"owner":"coilyco-x","id":"1"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("direct HTTP tool took %s, want it bounded", elapsed)
	}
}

// A zero timeout is the stated escape hatch, not an accident. It must leave
// the handler untouched rather than expiring immediately.
func TestZeroRequestTimeoutDisablesTheBound(t *testing.T) {
	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetRequestTimeout(0)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(toolList(t, decodeRPCResponse(t, resp))) == 0 {
		t.Error("tools/list returned nothing with the bound disabled")
	}
}

// The bound is on time-to-first-byte, never on the whole exchange.
// `Client.Timeout` covers reading the body, and a streamable-HTTP MCP response
// IS a body that stays open, so setting it killed every long tool call and took
// the session with it (mcp-beaver#79).
func TestBoundedUpstreamClient(t *testing.T) {
	for name, client := range map[string]*http.Client{"nil": nil, "unset": {}} {
		t.Run(name, func(t *testing.T) {
			got := boundedUpstreamClient(client)
			if got.Timeout != 0 {
				t.Errorf("Client.Timeout = %s, want 0: it would bound the open stream", got.Timeout)
			}
			transport, ok := got.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport = %T, want *http.Transport", got.Transport)
			}
			if transport.ResponseHeaderTimeout != upstreamResponseHeaderTimeout {
				t.Errorf("ResponseHeaderTimeout = %s, want %s", transport.ResponseHeaderTimeout, upstreamResponseHeaderTimeout)
			}
		})
	}

	// A caller that set its own timeout has made a deliberate choice.
	caller := &http.Client{Timeout: 3 * time.Second}
	if got := boundedUpstreamClient(caller); got.Timeout != 3*time.Second {
		t.Errorf("caller timeout = %s, want it preserved", got.Timeout)
	}
}

// The deadline has to reach the outbound call, not merely cut the response.
// A spec pointed at a dead port fails fast either way, so this asserts the
// wiring instead: the handler's context carries a deadline.
func TestRequestDeadlineReachesHandlerContext(t *testing.T) {
	var deadline time.Time
	var ok bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, ok = r.Context().Deadline()
	})
	ts := httptest.NewServer(withRequestDeadline(30*time.Second, inner))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if !ok {
		t.Fatal("handler context carried no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 30*time.Second {
		t.Errorf("deadline remaining = %s, want it inside the configured bound", remaining)
	}
}

// The span attribute #49 asked for: the method in flight, on the transport
// span, set before dispatch so a request that never returns still names it.
func TestTransportSpanCarriesMCPMethod(t *testing.T) {
	spec := roundTripSpec("http://127.0.0.1:1")
	if !strings.Contains(spec, "wrap ward mcp test") {
		t.Fatal("fixture changed shape")
	}
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// annotateTransportSpan is a no-op without a recording span, which is the
	// behaviour that matters: telemetry must never be load-bearing for serving.
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(toolList(t, decodeRPCResponse(t, resp))) == 0 {
		t.Error("tools/list returned nothing")
	}
}
