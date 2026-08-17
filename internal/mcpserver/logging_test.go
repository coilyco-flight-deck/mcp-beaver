package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type logLine struct {
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	Tool       string `json:"tool"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	DurationMS int64  `json:"duration_ms"`
}

// captureLogs points the process logger at a buffer for the duration of one
// test and returns what the served calls wrote.
func captureLogs(t *testing.T, run func()) []logLine {
	t.Helper()
	var buf bytes.Buffer
	SetLogOutput(&buf, "debug")
	t.Cleanup(func() { SetLogOutput(newDiscard(), "error") })

	run()

	var lines []logLine
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func newDiscard() *bytes.Buffer { return &bytes.Buffer{} }

func serveAndCall(t *testing.T, upstreamStatus int, body string) []logLine {
	t.Helper()
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "s3cr3t")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if upstreamStatus != http.StatusOK {
			http.Error(w, "upstream refused", upstreamStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	return captureLogs(t, func() {
		s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ts := httptest.NewServer(s.Handler())
		defer ts.Close()
		initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
		callTool(t, ts, initResp.Header.Get("Mcp-Session-Id"), "2", "get_thing",
			`{"owner":"coilyco-flight-deck","id":"42","search_query":"ramen"}`)
	})
}

func findLog(t *testing.T, lines []logLine, tool string) logLine {
	t.Helper()
	for _, line := range lines {
		if line.Tool == tool {
			return line
		}
	}
	t.Fatalf("no log line for tool %q in %+v", tool, lines)
	return logLine{}
}

// The whole point of #78: a served call leaves a server-side record. Twelve
// hours across twenty-two pods produced one log line fleet-wide.
func TestToolCallIsLogged(t *testing.T) {
	got := findLog(t, serveAndCall(t, http.StatusOK, `{"id":42}`), "get_thing")
	if got.Outcome != "ok" {
		t.Errorf("outcome = %q, want ok", got.Outcome)
	}
	if got.Level != "INFO" {
		t.Errorf("level = %q, want INFO - the ingest maps level onto OTel severity", got.Level)
	}
	if got.Msg == "" {
		t.Errorf("no message: %+v", got)
	}
}

// A refusal is the thing the issue was filed over. Playwright rejecting a call
// in 16ms is a server-side decision, and there was no server-side record of it
// anywhere.
func TestRefusedToolCallIsLoggedWithItsReason(t *testing.T) {
	got := findLog(t, serveAndCall(t, http.StatusBadGateway, ""), "get_thing")
	if got.Outcome != "tool_error" {
		t.Errorf("outcome = %q, want tool_error", got.Outcome)
	}
	if got.Level != "WARN" {
		t.Errorf("level = %q, want WARN", got.Level)
	}
	if !strings.Contains(got.Reason, "502") {
		t.Errorf("reason = %q, want it to name the upstream status", got.Reason)
	}
}

// The existing telemetry boundary never captures upstream URLs, and a refusal
// reason embeds one. Dropping the query keeps the reason actionable while
// removing the only two surfaces a credential reaches: `pin` writes query
// parameters, and `auth` writes a header.
func TestRefusalReasonDropsTheQueryString(t *testing.T) {
	got := findLog(t, serveAndCall(t, http.StatusBadGateway, ""), "get_thing")
	if strings.Contains(got.Reason, "query=ramen") {
		t.Fatalf("reason leaked a query value: %q", got.Reason)
	}
	if !strings.Contains(got.Reason, "<redacted>") {
		t.Errorf("reason = %q, want the dropped query marked rather than silently gone", got.Reason)
	}
}

func TestRedactReasonKeepsHostAndPath(t *testing.T) {
	got := redactReason(`GET https://www.example.com/r/golang/new/.rss?feed=abc123&user=kai -> 403 Forbidden`)
	for _, want := range []string{"www.example.com", "/r/golang/new/.rss", "403 Forbidden", "<redacted>"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted = %q, want it to keep %q", got, want)
		}
	}
	for _, unwanted := range []string{"abc123", "user=kai"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("redacted = %q, still carries %q", got, unwanted)
		}
	}
}

// An upstream that answers a rejection with an HTML error page would otherwise
// put the whole page in one log line.
func TestRedactReasonIsBounded(t *testing.T) {
	got := redactReason(strings.Repeat("x", maxLoggedReason*3))
	if len(got) > maxLoggedReason+3 {
		t.Fatalf("reason is %d characters, over the %d bound", len(got), maxLoggedReason)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a bounded reason did not say it was cut: %q", got[len(got)-10:])
	}
}
