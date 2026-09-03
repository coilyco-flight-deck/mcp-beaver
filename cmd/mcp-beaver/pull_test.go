package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hintedUpstream serves a read-only tool, a mutating one, and a silent one,
// which is the whole evidence axis `pull` sorts on.
func hintedUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0.1.0"}, nil)
	for _, tc := range []struct {
		name        string
		annotations *mcp.ToolAnnotations
	}{
		{"get_thing", &mcp.ToolAnnotations{ReadOnlyHint: true}},
		{"delete_thing", &mcp.ToolAnnotations{ReadOnlyHint: false}},
		{"mystery", nil},
	} {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        tc.name,
			Description: tc.name,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Annotations: tc.annotations,
		}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	return httptest.NewServer(mux)
}

func fakeRegistryFor(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v0/servers/test%2Fthings/versions/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"server": map[string]any{
				"name":        "test/things",
				"description": "Things.",
				"remotes":     []map[string]string{{"type": "streamable-http", "url": upstreamURL}},
			},
			"_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active"}},
		})
	}))
}

// The whole loop: registry entry in, guardfile out, and that guardfile is one
// `lint`, `lint-upstream --read-only strict`, and `serve-upstream` all accept.
func TestPullWritesAGuardfileTheOtherVerbsAccept(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	registry := fakeRegistryFor(t, upstream.URL+"/mcp")
	defer registry.Close()

	path := filepath.Join(t.TempDir(), "things.mcp.kdl")
	var out bytes.Buffer
	if err := runPull(context.Background(), &out, []string{"test/things", "--registry", registry.URL, "-o", path}); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("-o wrote to stdout too: %q", out.String())
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, want := range []string{`mcp-upstream "test/things"`, "annotation-coverage partial annotated=2 silent=1", `can "get_thing"`} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(string(text), "delete_thing") || strings.Contains(string(text), "mystery") {
		t.Fatalf("read-only scope leaked an unasserted or mutating tool:\n%s", text)
	}

	out.Reset()
	if err := runLint(&out, []string{path}); err != nil {
		t.Fatalf("runLint: %v", err)
	}
	if got := out.String(); got != "get_thing\n" {
		t.Fatalf("lint stdout = %q", got)
	}
	out.Reset()
	if err := runLint(&out, []string{path, "--methods"}); err != nil || out.String() != "get_thing\t-\n" {
		t.Fatalf("lint --methods = %q, %v", out.String(), err)
	}

	out.Reset()
	if err := runLintUpstream(context.Background(), &out, []string{path, "--read-only", "strict"}); err != nil {
		t.Fatalf("lint-upstream strict on the generated file: %v", err)
	}
	if got := out.String(); got != "get_thing\n" {
		t.Fatalf("lint-upstream stdout = %q", got)
	}
	err = runLintUpstream(context.Background(), &out, []string{path, "--tool", "mystery"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("a file beside --tool must be refused, got %v", err)
	}

	// serve-upstream reads the same file: build what it would serve, minus
	// the listener, through the inputs it resolves.
	inputs, err := resolveProxyInputs("serve-upstream", path, upstreamFlagInputs{})
	if err != nil {
		t.Fatalf("resolveProxyInputs: %v", err)
	}
	if inputs.name != "things" || inputs.upstream != upstream.URL+"/mcp" || strings.Join(inputs.tools, ",") != "get_thing" || inputs.instructions != "Things." {
		t.Fatalf("inputs = %+v", inputs)
	}
}

func TestPullScopesWidenWithoutReordering(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	var previous []string
	for _, scope := range []string{"read-only", "read-write", "all"} {
		var out bytes.Buffer
		if err := runPull(context.Background(), &out, []string{"test/things", "--upstream", upstream.URL + "/mcp", "--scope", scope}); err != nil {
			t.Fatalf("runPull %s: %v", scope, err)
		}
		var grants []string
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "can ") {
				grants = append(grants, strings.TrimSpace(line))
			}
		}
		if len(grants) <= len(previous) || !inOrder(previous, grants) {
			t.Fatalf("scope %s = %v is not an ordered superset of %v", scope, grants, previous)
		}
		previous = grants
	}
	if len(previous) != 3 {
		t.Fatalf("all exposed %d grants, want 3", len(previous))
	}
}

// inOrder reports whether narrow appears inside wide in the same order, which
// is what widening a scope by adding lines and moving none means.
func inOrder(narrow, wide []string) bool {
	i := 0
	for _, line := range wide {
		if i < len(narrow) && narrow[i] == line {
			i++
		}
	}
	return i == len(narrow)
}

func TestPullRejectsBadArguments(t *testing.T) {
	for name, argv := range map[string][]string{
		"no name":   {"--scope", "read-only"},
		"two names": {"a/b", "c/d"},
		"bad scope": {"a/b", "--upstream", "http://127.0.0.1:1/mcp", "--scope", "everything"},
	} {
		if err := runPull(context.Background(), &bytes.Buffer{}, argv); err == nil {
			t.Fatalf("%s: accepted %v", name, argv)
		}
	}
}

// The thirty guardfiles the registry-pull prototype generated are the corpus
// this grammar was measured on, so every one of them must parse, the
// thirteen that expose nothing included.
func TestLintAcceptsThePrototypeCorpus(t *testing.T) {
	specs, err := filepath.Glob(filepath.Join("..", "..", "scripts", "registry-probe", "guardfiles", "*.mcp.kdl"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	if len(specs) != 30 {
		t.Fatalf("found %d corpus guardfiles, want 30", len(specs))
	}
	for _, spec := range specs {
		src, err := os.ReadFile(spec)
		if err != nil {
			t.Fatalf("read %s: %v", spec, err)
		}
		var out bytes.Buffer
		if err := runLint(&out, []string{spec}); err != nil {
			t.Fatalf("%s: %v", filepath.Base(spec), err)
		}
		// A file that states grants lints to them, and one that states none
		// lints to nothing rather than failing: exposing nothing is the
		// correct output for an upstream that declares nothing.
		if grants := strings.Count(string(src), "\n    can "); grants != strings.Count(out.String(), "\n") {
			t.Fatalf("%s states %d grants and lints %q", filepath.Base(spec), grants, out.String())
		}
	}
}
