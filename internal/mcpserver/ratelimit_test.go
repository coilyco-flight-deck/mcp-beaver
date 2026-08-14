package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseRateLimitShapes(t *testing.T) {
	for _, spec := range []string{"1/1s", "10/1m", " 5 / 2s "} {
		if _, err := newRateLimiter(spec); err != nil {
			t.Errorf("newRateLimiter(%q) = %v, want it accepted", spec, err)
		}
	}
	for _, spec := range []string{"", "1", "0/1s", "-1/1s", "1/0s", "1/banana", "banana/1s", "1/-1s"} {
		if _, err := newRateLimiter(spec); err == nil {
			t.Errorf("newRateLimiter(%q) was accepted, want fail-closed", spec)
		}
	}
}

func TestRateLimitFailsClosed(t *testing.T) {
	for name, node := range map[string]string{
		"duplicate node":    "rate-limit \"1/1s\"\nrate-limit \"2/1s\"",
		"property form":     `rate-limit rate="1/1s"`,
		"no argument":       `rate-limit`,
		"malformed rate":    `rate-limit "one per second"`,
		"two arguments":     `rate-limit "1/1s" "2/1s"`,
		"zero count":        `rate-limit "0/1s"`,
		"negative duration": `rate-limit "1/-1s"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
			if err == nil {
				t.Fatal("New accepted a malformed `rate-limit` node")
			}
		})
	}
}

// The property that matters: two concurrent calls do not both reach the
// upstream inside the same window. MusicBrainz at 1 req/sec is the case, and
// "two concurrent turns exceed it" is exactly the failure that passes in
// testing and 503s under real channel traffic.
func TestRateLimitSerialisesUpstreamCalls(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "test-token")

	var mu sync.Mutex
	var arrivals []time.Time
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	spec := `rate-limit "1/200ms"` + "\n\n" + roundTripSpec(upstream.URL)
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postToServer(t, ts.Client(), ts.URL+"/mcp", "",
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-x","id":"1"}}}`)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 3 {
		t.Fatalf("upstream saw %d requests, want 3", len(arrivals))
	}
	// Burst of 1 means the second and third are spaced by the window. Allow
	// generous slack for scheduling; the unlimited case would be near-zero.
	spread := arrivals[len(arrivals)-1].Sub(arrivals[0])
	if spread < 300*time.Millisecond {
		t.Errorf("three calls spread over %s, want them serialised by the 200ms bucket", spread)
	}
}

// No node, no bucket. An existing guardfile must not start queueing.
func TestRateLimitAbsentByDefault(t *testing.T) {
	limiter, err := parseRateLimit([]byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("parseRateLimit: %v", err)
	}
	if limiter != nil {
		t.Error("a guardfile with no `rate-limit` node got a bucket")
	}
}

// A queued call must still respect the request deadline rather than holding a
// slot forever. This is the interaction between #57 and #49.
func TestRateLimitWaitRespectsDeadline(t *testing.T) {
	limiter, err := newRateLimiter("1/1h")
	if err != nil {
		t.Fatalf("newRateLimiter: %v", err)
	}
	called := 0
	handler := withRateLimit(limiter, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called++
		return &mcp.CallToolResult{}, nil
	})
	// First call consumes the single burst token.
	if _, err := handler(context.Background(), &mcp.CallToolRequest{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := handler(ctx, &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("second call returned a transport error: %v", err)
	}
	if !res.IsError {
		t.Error("a call queued past its deadline reported success")
	}
	if called != 1 {
		t.Errorf("upstream handler ran %d times, want 1: the queued call must not go out", called)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(strings.ToLower(text), "rate limit") {
		t.Errorf("error text = %q, want it to name the rate limit", text)
	}
}
