package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// writeCountingUpstream reports how many writes actually reached it, which is
// the number this control exists to keep at zero for a blank call.
func writeCountingUpstream(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	writes := &atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	t.Cleanup(ts.Close)
	return ts, writes
}

// The reported shape: four fully blank messages posted into a shared channel,
// every layer reporting success. See sirens-echo#1035.
func TestRejectEmptyArgumentRefusesABlankWrite(t *testing.T) {
	upstream, writes := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL,
		`reject-empty-argument "create_thing" field="title"`))

	result := callTool(t, ts, sessionID, "2", "create_thing", `{"title":""}`)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("a blank write was accepted: %v", result)
	}
	if got := writes.Load(); got != 0 {
		t.Errorf("upstream writes = %d, want 0: the refusal must happen before the call", got)
	}
	if got := firstText(t, result); !strings.Contains(got, "empty") {
		t.Errorf("refusal = %q, want it to name the reason", got)
	}
}

// Whitespace is the same blank once it reaches a channel.
func TestRejectEmptyArgumentRefusesWhitespace(t *testing.T) {
	upstream, writes := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL,
		`reject-empty-argument "create_thing" field="title"`))

	result := callTool(t, ts, sessionID, "2", "create_thing", `{"title":"   \n  "}`)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("a whitespace-only write was accepted: %v", result)
	}
	if got := writes.Load(); got != 0 {
		t.Errorf("upstream writes = %d, want 0", got)
	}
}

// A real write must still land, or the control is an outage.
func TestRejectEmptyArgumentPassesARealWrite(t *testing.T) {
	upstream, writes := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL,
		`reject-empty-argument "create_thing" field="title"`))

	result := callTool(t, ts, sessionID, "2", "create_thing", `{"title":"ramen"}`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("a real write was refused: %v", result)
	}
	if got := writes.Load(); got != 1 {
		t.Errorf("upstream writes = %d, want 1", got)
	}
}

// Off unless declared, so every guardfile that omits it behaves as it does now.
func TestRejectEmptyArgumentIsOffByDefault(t *testing.T) {
	upstream, writes := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, ""))

	result := callTool(t, ts, sessionID, "2", "create_thing", `{"title":""}`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Errorf("an undeclared tool refused a blank write: %v", result)
	}
	if got := writes.Load(); got != 1 {
		t.Errorf("upstream writes = %d, want 1 when the control is absent", got)
	}
}

// Absence is the grant's error to report, and it says so better than this
// would. Only a field that arrives present and blank is this control's.
func TestRejectEmptyArgumentIgnoresAnAbsentField(t *testing.T) {
	upstream, _ := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL,
		`reject-empty-argument "get_thing" field="q"`))

	result := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
	if got := firstText(t, result); strings.Contains(got, "reject-empty-argument") {
		t.Errorf("an absent optional field was refused by this control: %q", got)
	}
}

// The same line the result control draws, so the two agree about what empty is.
func TestRejectEmptyArgumentKeepsFalseAndZero(t *testing.T) {
	upstream, writes := writeCountingUpstream(t)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL,
		`reject-empty-argument "create_thing" field="title"`))

	// A string grant takes a string, so this asserts the shared predicate
	// rather than the schema: "0" and "false" are content, not absence.
	for _, title := range []string{`"0"`, `"false"`} {
		result := callTool(t, ts, sessionID, "2", "create_thing", `{"title":`+title+`}`)
		if isErr, _ := result["isError"].(bool); isErr {
			t.Errorf("title %s was refused; it is content, not an absence", title)
		}
	}
	if got := writes.Load(); got != 2 {
		t.Errorf("upstream writes = %d, want 2", got)
	}
}

// Matching `cache` and `pin`: a name nothing serves fails the build rather
// than guarding nothing.
func TestRejectEmptyArgumentRefusesUnknownNames(t *testing.T) {
	upstream, _ := writeCountingUpstream(t)
	for _, siblings := range []string{
		`reject-empty-argument "no_such_tool" field="title"`,
		`reject-empty-argument "create_thing" field="no_such_field"`,
		`reject-empty-argument "create_thing"`,
		"reject-empty-argument \"create_thing\" field=\"title\"\nreject-empty-argument \"create_thing\" field=\"title\"",
	} {
		if _, err := New("test", "test.mcp.kdl",
			[]byte(cachedSpec(upstream.URL, siblings))); err == nil {
			t.Errorf("%q built successfully", siblings)
		}
	}
}
