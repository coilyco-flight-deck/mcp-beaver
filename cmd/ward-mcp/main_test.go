package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/mcpserver"
)

func TestLintPrintsProjectedToolNames(t *testing.T) {
	var out bytes.Buffer
	if err := runLint(&out, []string{filepath.Join("testdata", "valid.mcp.kdl")}); err != nil {
		t.Fatalf("runLint: %v", err)
	}
	// Sorted, one per line: the list a consumer diffs against a reviewed
	// expectation instead of reimplementing the parse to derive it.
	if got, want := out.String(), "get_issue\nlist_issue\n"; got != want {
		t.Fatalf("runLint stdout = %q, want %q", got, want)
	}
}

func TestLintRejectsMalformedSpec(t *testing.T) {
	var out bytes.Buffer
	err := runLint(&out, []string{filepath.Join("testdata", "malformed.mcp.kdl")})
	if err == nil {
		t.Fatal("runLint accepted a malformed spec")
	}
	if !strings.Contains(err.Error(), "malformed.mcp.kdl") || !strings.Contains(err.Error(), "parse KDL") {
		t.Fatalf("runLint error = %q, want the spec path and the parse failure", err)
	}
	if out.Len() != 0 {
		t.Fatalf("runLint wrote %q to stdout for a rejected spec", out.String())
	}
}

// A spec can be well-formed KDL and still mint a broken surface. Linting
// through mcpserver.New rather than opcore.ParseInline is what catches it.
func TestLintRejectsDuplicateToolProjection(t *testing.T) {
	var out bytes.Buffer
	err := runLint(&out, []string{filepath.Join("testdata", "duplicate-tool.mcp.kdl")})
	if err == nil {
		t.Fatal("runLint accepted two grants projecting one tool name")
	}
	if !strings.Contains(err.Error(), `duplicate tool name "get_issue"`) {
		t.Fatalf("runLint error = %q, want the duplicate tool name", err)
	}
	if out.Len() != 0 {
		t.Fatalf("runLint wrote %q to stdout for a rejected spec", out.String())
	}
}

func TestLintRejectsUnreadableSpec(t *testing.T) {
	var out bytes.Buffer
	err := runLint(&out, []string{filepath.Join("testdata", "does-not-exist.mcp.kdl")})
	if err == nil {
		t.Fatal("runLint accepted a missing spec path")
	}
	if !strings.Contains(err.Error(), "read spec") {
		t.Fatalf("runLint error = %q, want a read failure", err)
	}
}

func TestLintNeedsExactlyOneSpecPath(t *testing.T) {
	for name, argv := range map[string][]string{
		"none": {},
		"two":  {filepath.Join("testdata", "valid.mcp.kdl"), filepath.Join("testdata", "valid.mcp.kdl")},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runLint(&out, argv); err == nil {
				t.Fatal("runLint accepted the wrong number of spec paths")
			}
		})
	}
}

// The shipped examples are real configuration, so they are validated through
// this surface rather than restated by a second parser. Only the exit status
// is asserted: pinning the exact tool names here would be the antipattern the
// command exists to remove. examples/ holds serve-mode guardfiles; an SSM
// policy uses a different grammar and is not lintable through this path.
func TestLintAcceptsShippedExamples(t *testing.T) {
	specs, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.mcp.kdl"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("found no example specs to lint")
	}
	for _, spec := range specs {
		t.Run(filepath.Base(spec), func(t *testing.T) {
			var out bytes.Buffer
			if err := runLint(&out, []string{spec}); err != nil {
				t.Fatalf("runLint: %v", err)
			}
			if out.Len() == 0 {
				t.Fatal("runLint reported no tools for a shipped example")
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	err := run([]string{"validate"})
	if err == nil {
		t.Fatal("run accepted an unknown command")
	}
	if !strings.Contains(err.Error(), "lint") {
		t.Fatalf("run error = %q, want lint listed among the commands", err)
	}
}

func TestServeHTTPShutsDownOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serveHTTP(ctx, "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatalf("serveHTTP: %v", err)
	}
}

func TestConnectProxyWithRetryRecovers(t *testing.T) {
	attempts := 0
	srv, err := connectProxyWithRetry(context.Background(), time.Second, time.Millisecond, func(context.Context) (*mcpserver.Server, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("upstream is warming")
		}
		return &mcpserver.Server{}, nil
	})
	if err != nil {
		t.Fatalf("connectProxyWithRetry: %v", err)
	}
	if srv == nil {
		t.Fatal("connectProxyWithRetry returned a nil server")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestConnectProxyWithRetryTimesOut(t *testing.T) {
	_, err := connectProxyWithRetry(context.Background(), 20*time.Millisecond, time.Millisecond, func(context.Context) (*mcpserver.Server, error) {
		return nil, errors.New("upstream unavailable")
	})
	if err == nil {
		t.Fatal("connectProxyWithRetry succeeded, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("connectProxyWithRetry error = %q", err)
	}
}
