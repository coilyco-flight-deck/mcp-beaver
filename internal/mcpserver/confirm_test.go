package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// countingUpstream answers any request with an empty JSON object and tallies
// the hits, so a test can assert the upstream was or was not reached.
func countingUpstream(hits *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
}

func confirmSpec(upstream string) string {
	return `confirm "create_thing" message="This creates a thing upstream. Continue?"` + "\n\n" + roundTripSpec(upstream)
}

// connectSDKClient drives the server through the real SDK client, which owns
// the multi round-trip retry loop, so the test exercises the protocol flow
// rather than the handler in isolation.
func connectSDKClient(t *testing.T, ts *httptest.Server, elicit func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "confirm-test", Version: "0.1.0"}, &mcp.ClientOptions{
		ElicitationHandler: elicit,
	})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func confirmServer(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(confirmSpec(upstream)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// An accepted confirmation runs the tool: the upstream is reached exactly once,
// on the retry, never on the first call.
func TestConfirmAcceptedRunsTheToolOnce(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "confirm-test-token")
	var upstreamHits int
	upstream := httptest.NewServer(countingUpstream(&upstreamHits))
	defer upstream.Close()

	ts := confirmServer(t, upstream.URL)
	var prompted string
	session := connectSDKClient(t, ts, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompted = req.Params.Message
		return &mcp.ElicitResult{Action: "accept"}, nil
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_thing",
		Arguments: map[string]any{"owner": "coilyco-flight-deck", "title": "t"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored after an accepted confirmation: %s", contentText(res))
	}
	if !strings.Contains(prompted, "creates a thing upstream") {
		t.Errorf("prompt = %q, want the declared message", prompted)
	}
	if upstreamHits != 1 {
		t.Errorf("upstream hits = %d, want exactly 1", upstreamHits)
	}
}

// A declined confirmation must not reach the upstream at all. This is the
// property that makes the gate worth having.
func TestConfirmDeclinedNeverReachesUpstream(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(countingUpstream(&upstreamHits))
	defer upstream.Close()

	ts := confirmServer(t, upstream.URL)
	session := connectSDKClient(t, ts, func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "decline"}, nil
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_thing",
		Arguments: map[string]any{"owner": "coilyco-flight-deck", "title": "t"},
	})
	if err == nil && !res.IsError {
		t.Fatalf("declining still produced a success result: %+v", res)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hits = %d, want 0 for a declined confirmation", upstreamHits)
	}
}

// An ungated tool in the same spec keeps its single-round-trip behaviour, so
// opting one tool in does not change the others.
func TestUngatedToolIsNotConfirmed(t *testing.T) {
	t.Setenv("WARD_MCP_TEST_TOKEN", "confirm-test-token")
	var upstreamHits int
	upstream := httptest.NewServer(countingUpstream(&upstreamHits))
	defer upstream.Close()

	ts := confirmServer(t, upstream.URL)
	var prompts int
	session := connectSDKClient(t, ts, func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		prompts++
		return &mcp.ElicitResult{Action: "accept"}, nil
	})

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_thing",
		Arguments: map[string]any{"owner": "coilyco-flight-deck", "id": "1"},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if prompts != 0 {
		t.Errorf("ungated tool raised %d confirmation prompts, want 0", prompts)
	}
}

// A confirmation attached to a tool the spec does not mint is the dangerous
// typo: the author believes a destructive tool is gated when it is not.
func TestConfirmFailsClosedOnUnknownTool(t *testing.T) {
	_, err := New("test", "test.mcp.kdl",
		[]byte(`confirm "delete_thing"`+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
	if err == nil {
		t.Fatal("New accepted a confirm for a tool the spec does not mint")
	}
	if !strings.Contains(err.Error(), "delete_thing") {
		t.Errorf("error = %q, want it to name the tool", err)
	}
}

func TestConfirmFailsClosedOnMalformedNode(t *testing.T) {
	for name, node := range map[string]string{
		"unknown property": `confirm "create_thing" colour="red"`,
		"empty message":    `confirm "create_thing" message=""`,
		"duplicate":        "confirm \"create_thing\"\nconfirm \"create_thing\"",
		"no argument":      `confirm`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1"))); err == nil {
				t.Fatal("New accepted a malformed `confirm` node")
			}
		})
	}
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
