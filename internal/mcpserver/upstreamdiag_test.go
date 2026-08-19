package mcpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SDK surfaces only a status line, so the upstream's own explanation was
// discarded unread. Three investigations of mcp-beaver#85 inferred a cause from
// "Bad Request" alone.
func TestUpstreamRejectionLogsTheServersOwnReason(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Bad Request: Unsupported protocol version (supported versions: 2025-03-26)", http.StatusBadRequest)
	}))
	defer upstream.Close()

	var logged bytes.Buffer
	previous := logger
	logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	t.Cleanup(func() { logger = previous })

	client := withUpstreamDiagnostics(upstream.Client())
	req, err := http.NewRequest(http.MethodPost, upstream.URL+"/mcp", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Mcp-Session-Id", "session-under-test")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()

	// The body is still readable, so this stays an observer rather than a
	// consumer of the response the caller is waiting for.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Unsupported protocol version") {
		t.Fatalf("the caller lost the body: %q", body)
	}
	for _, want := range []string{
		"Unsupported protocol version",
		"session-under-test",
		"400",
	} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log does not carry %q:\n%s", want, logged.String())
		}
	}
}

// A healthy response must not be touched at all.
func TestUpstreamDiagnosticsIgnoresSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fine"))
	}))
	defer upstream.Close()

	var logged bytes.Buffer
	previous := logger
	logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	t.Cleanup(func() { logger = previous })

	resp, err := withUpstreamDiagnostics(upstream.Client()).Get(upstream.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fine" {
		t.Fatalf("body = %q", body)
	}
	if logged.Len() != 0 {
		t.Fatalf("a healthy response was logged:\n%s", logged.String())
	}
}
