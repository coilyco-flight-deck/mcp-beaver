package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bodyUpstream answers every request with one fixed body, which is the only
// variable this control reads.
func bodyUpstream(t *testing.T, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// The reported shape: Dowel posted four blank messages because an empty answer
// is indistinguishable from a real one. See sirens-echo#1035.
func TestRejectEmptyRefusesAnEmptyAnswer(t *testing.T) {
	upstream := bodyUpstream(t, `{}`)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `reject-empty "get_thing"`))

	result := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("an empty answer was returned rather than refused: %v", result)
	}
	if got := firstText(t, result); !strings.Contains(got, "answered with nothing") {
		t.Errorf("refusal = %q, want it to name the reason", got)
	}
}

// Off unless declared, so every guardfile that omits it behaves as it does now.
func TestRejectEmptyIsOffByDefault(t *testing.T) {
	upstream := bodyUpstream(t, `{}`)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, ""))

	result := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Errorf("an undeclared tool refused an empty answer: %v", result)
	}
}

// A real answer must still pass, or the control is just an outage.
func TestRejectEmptyPassesAnAnswerThrough(t *testing.T) {
	upstream := bodyUpstream(t, `{"title":"ramen"}`)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `reject-empty "get_thing"`))

	result := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("a real answer was refused: %v", result)
	}
	if got := firstText(t, result); !strings.Contains(got, "ramen") {
		t.Errorf("result = %q, want the upstream body", got)
	}
}

// The line this control is most likely to get wrong. A tool answering "no" or
// "none" has answered, and refusing that turns a correct result into an error.
func TestRejectEmptyKeepsFalseAndZero(t *testing.T) {
	for _, body := range []string{`false`, `0`, `{"found":false}`, `{"count":0}`} {
		upstream := bodyUpstream(t, body)
		ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `reject-empty "get_thing"`))
		result := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
		if isErr, _ := result["isError"].(bool); isErr {
			t.Errorf("body %s was refused; false and zero are answers, not absences", body)
		}
	}
}

// Whitespace, null, and the empty containers are the agreed set.
func TestEmptyResultCoversTheDeclaredSet(t *testing.T) {
	empty := []string{"", "   \n\t ", "null", `""`, `"  "`, "[]", "{}"}
	for _, body := range empty {
		result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
		if !emptyResult(result) {
			t.Errorf("%q should count as empty", body)
		}
	}
	answers := []string{"false", "0", `{"a":1}`, "[null]", "hello", `"x"`}
	for _, body := range answers {
		result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
		if emptyResult(result) {
			t.Errorf("%q should count as an answer", body)
		}
	}
	if !emptyResult(&mcp.CallToolResult{}) {
		t.Error("a result with no content at all should count as empty")
	}
}

// An upstream that already failed keeps its own reason, which is more useful
// than this one.
func TestRejectEmptyLeavesAnExistingErrorAlone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	server, sessionID := serveSpec(t, cachedSpec(ts.URL, `reject-empty "get_thing"`))

	result := callTool(t, server, sessionID, "2", "get_thing", `{"id":"42"}`)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("a failing upstream should still be an error: %v", result)
	}
	if got := firstText(t, result); strings.Contains(got, "answered with nothing") {
		t.Errorf("the upstream's own reason was replaced by this control's: %q", got)
	}
}

// Matching `cache`: naming a tool the spec does not serve fails the build
// rather than silently guarding nothing.
func TestRejectEmptyRefusesAnUnservedTool(t *testing.T) {
	upstream := bodyUpstream(t, `{}`)
	_, err := New("test", "test.mcp.kdl",
		[]byte(cachedSpec(upstream.URL, `reject-empty "no_such_tool"`)))
	if err == nil {
		t.Fatal("a reject-empty naming an unserved tool built successfully")
	}
	if !strings.Contains(err.Error(), "not a grant-backed tool") {
		t.Errorf("error = %v, want it to name the unserved tool", err)
	}
}

// The guardfile must not be able to say the same thing twice, and a property
// is a typo rather than a feature.
func TestRejectEmptyRefusesDuplicatesAndProperties(t *testing.T) {
	upstream := bodyUpstream(t, `{}`)
	for _, siblings := range []string{
		"reject-empty \"get_thing\"\nreject-empty \"get_thing\"",
		`reject-empty "get_thing" ttl="15m"`,
	} {
		if _, err := New("test", "test.mcp.kdl",
			[]byte(cachedSpec(upstream.URL, siblings))); err == nil {
			t.Errorf("%q built successfully", siblings)
		}
	}
}
