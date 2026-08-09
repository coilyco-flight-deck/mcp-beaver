package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestLintUpstreamPrintsSortedAllowlist(t *testing.T) {
	var out bytes.Buffer
	argv := []string{"--tool", "signoz_list_metrics", "--tool", "signoz_get_alert"}
	if err := runLintUpstream(context.Background(), &out, argv); err != nil {
		t.Fatalf("runLintUpstream: %v", err)
	}
	// Same contract as `lint`: the reviewed surface, sorted, one per line.
	if got, want := out.String(), "signoz_get_alert\nsignoz_list_metrics\n"; got != want {
		t.Fatalf("runLintUpstream stdout = %q, want %q", got, want)
	}
}

func TestLintUpstreamRejectsMalformedAllowlist(t *testing.T) {
	for name, argv := range map[string][]string{
		"no tools":  {},
		"duplicate": {"--tool", "a", "--tool", "a"},
		"empty":     {"--tool", "a", "--tool", "  "},
		"stray arg": {"--tool", "a", "values.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runLintUpstream(context.Background(), &out, argv); err == nil {
				t.Fatal("runLintUpstream accepted a malformed allowlist")
			}
			if out.Len() != 0 {
				t.Fatalf("runLintUpstream wrote %q to stdout for a rejected allowlist", out.String())
			}
		})
	}
}

// The offline gate the issue asked for: a mutation tool entering a read-only
// allowlist fails review without any upstream connection.
func TestLintUpstreamHeuristicRejectsMutationNames(t *testing.T) {
	var out bytes.Buffer
	argv := []string{
		"--read-only", "heuristic",
		"--tool", "signoz_list_metrics",
		"--tool", "signoz_create_dashboard",
	}
	err := runLintUpstream(context.Background(), &out, argv)
	if err == nil {
		t.Fatal("runLintUpstream accepted a mutation tool in a read-only allowlist")
	}
	if !strings.Contains(err.Error(), "signoz_create_dashboard") {
		t.Fatalf("error = %q, want it to name the offending tool", err)
	}
	if !strings.Contains(err.Error(), "--read-only strict") {
		t.Fatalf("error = %q, want it to point at the authoritative check", err)
	}
}

func TestLintUpstreamHeuristicAcceptsReadOnlyNames(t *testing.T) {
	var out bytes.Buffer
	argv := []string{
		"--read-only", "heuristic",
		"--tool", "signoz_list_metrics",
		"--tool", "signoz_get_field_values",
	}
	if err := runLintUpstream(context.Background(), &out, argv); err != nil {
		t.Fatalf("runLintUpstream rejected a read-only allowlist: %v", err)
	}
}

// strict is upstream truth, so without an upstream it is not a weaker check,
// it is no check. Failing beats silently downgrading to the heuristic.
func TestLintUpstreamStrictNeedsAnUpstream(t *testing.T) {
	var out bytes.Buffer
	err := runLintUpstream(context.Background(), &out, []string{"--read-only", "strict", "--tool", "a"})
	if err == nil {
		t.Fatal("runLintUpstream accepted --read-only strict without --upstream")
	}
	if !strings.Contains(err.Error(), "--upstream") {
		t.Fatalf("error = %q, want it to name the missing flag", err)
	}
}

func TestLintUpstreamRejectsUnknownReadOnlyMode(t *testing.T) {
	var out bytes.Buffer
	err := runLintUpstream(context.Background(), &out, []string{"--read-only", "yes", "--tool", "a"})
	if err == nil {
		t.Fatal("runLintUpstream accepted an unknown --read-only mode")
	}
	if !strings.Contains(err.Error(), "heuristic") {
		t.Fatalf("error = %q, want it to list the valid modes", err)
	}
}

// upstreamWithAnnotations serves two tools, only one annotated read-only, so
// the strict path has something real to reject.
func upstreamWithAnnotations(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0.1.0"}, nil)
	for _, tc := range []struct {
		name     string
		readOnly bool
	}{{"fetch_report", true}, {"rotate_key", false}} {
		tool := &mcp.Tool{
			Name:        tc.name,
			Description: tc.name,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}
		if tc.readOnly {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		mcp.AddTool(srv, tool, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	return httptest.NewServer(mux)
}

// The authoritative check: `rotate_key` clears the name heuristic but the
// upstream never annotates it readOnlyHint, so strict rejects it.
func TestLintUpstreamStrictRejectsUnannotatedTool(t *testing.T) {
	ts := upstreamWithAnnotations(t)
	defer ts.Close()

	var out bytes.Buffer
	argv := []string{
		"--upstream", ts.URL + "/mcp",
		"--read-only", "strict",
		"--tool", "fetch_report",
		"--tool", "rotate_key",
	}
	err := runLintUpstream(context.Background(), &out, argv)
	if err == nil {
		t.Fatal("runLintUpstream accepted a tool the upstream never marked read-only")
	}
	if !strings.Contains(err.Error(), "rotate_key") {
		t.Fatalf("error = %q, want it to name the unannotated tool", err)
	}
	if strings.Contains(err.Error(), "fetch_report") {
		t.Fatalf("error = %q, want it to spare the annotated tool", err)
	}
}

func TestLintUpstreamStrictAcceptsAnnotatedAllowlist(t *testing.T) {
	ts := upstreamWithAnnotations(t)
	defer ts.Close()

	var out bytes.Buffer
	argv := []string{
		"--upstream", ts.URL + "/mcp",
		"--read-only", "strict",
		"--tool", "fetch_report",
	}
	if err := runLintUpstream(context.Background(), &out, argv); err != nil {
		t.Fatalf("runLintUpstream rejected an annotated read-only allowlist: %v", err)
	}
	if got, want := out.String(), "fetch_report\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// Connecting also inherits the proxy's existence check, so a typo'd tool name
// fails against the upstream even without --read-only.
func TestLintUpstreamRejectsToolMissingUpstream(t *testing.T) {
	ts := upstreamWithAnnotations(t)
	defer ts.Close()

	var out bytes.Buffer
	argv := []string{"--upstream", ts.URL + "/mcp", "--tool", "fetch_reprot"}
	err := runLintUpstream(context.Background(), &out, argv)
	if err == nil {
		t.Fatal("runLintUpstream accepted a tool the upstream does not expose")
	}
	if !strings.Contains(err.Error(), "fetch_reprot") {
		t.Fatalf("error = %q, want it to name the missing tool", err)
	}
}
