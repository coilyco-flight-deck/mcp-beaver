package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const upstreamSpecFull = `instructions {
    text "Search the published Tandem docs index."
}

wrap mcp upstream "ac.tandem/docs-mcp" {
    url "https://tandem.ac/mcp"
    transport streamable-http
    annotation-coverage partial annotated=7 silent=6

    auth header-token {
        header "Authorization"
        prefix "Bearer "
        value literal "unused"
    }

    can "search_docs"
    can get_doc
}
`

func TestParseUpstreamSpecReadsEveryNode(t *testing.T) {
	spec, err := ParseUpstreamSpec("tandem.mcp.kdl", []byte(upstreamSpecFull))
	if err != nil {
		t.Fatalf("ParseUpstreamSpec: %v", err)
	}
	if spec.Name != "ac.tandem/docs-mcp" || spec.URL != "https://tandem.ac/mcp" {
		t.Fatalf("name/url = %q %q", spec.Name, spec.URL)
	}
	// Stated order, quoted and bare alike: the proxy serves it as written.
	if got := strings.Join(spec.Tools, ","); got != "search_docs,get_doc" {
		t.Fatalf("tools = %q", got)
	}
	if spec.Coverage == nil || *spec.Coverage != (AnnotationCoverage{Kind: "partial", Annotated: 7, Silent: 6}) {
		t.Fatalf("coverage = %+v", spec.Coverage)
	}
	if len(spec.Headers) != 1 || spec.Headers[0].Name != "Authorization" {
		t.Fatalf("headers = %+v", spec.Headers)
	}
	if spec.Instructions != "Search the published Tandem docs index." {
		t.Fatalf("instructions = %q", spec.Instructions)
	}
}

func TestParseUpstreamSpecAllowsAnEmptyAllowlist(t *testing.T) {
	spec, err := ParseUpstreamSpec("", []byte(`wrap mcp upstream "x/y" {
    url "https://example.invalid/mcp"
    annotation-coverage undeclared annotated=0 silent=3
}`))
	if err != nil {
		t.Fatalf("a guardfile that exposes nothing is a valid statement: %v", err)
	}
	if len(spec.Tools) != 0 || spec.Coverage.Kind != "undeclared" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestIsUpstreamSpecTellsTheKindsApart(t *testing.T) {
	for src, want := range map[string]bool{
		`wrap mcp upstream "x/y" { url "https://e.invalid/mcp" }`: true,
		`wrap ward mcp forgejo { base-url "https://e.invalid" }`:  false,
		`instructions { text "hi" }
wrap mcp upstream "x/y" { url "https://e.invalid/mcp" }`: true,
	} {
		got, err := IsUpstreamSpec([]byte(src))
		if err != nil {
			t.Fatalf("IsUpstreamSpec(%q): %v", src, err)
		}
		if got != want {
			t.Fatalf("IsUpstreamSpec(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestParseUpstreamSpecFailsClosed(t *testing.T) {
	body := func(inner string) string {
		return "wrap mcp upstream \"x/y\" {\n    url \"https://e.invalid/mcp\"\n" + inner + "\n}"
	}
	cases := map[string]string{
		"no url":                        `wrap mcp upstream "x/y" { can "a" }`,
		"relative url":                  body(`url "e.invalid/mcp"`),
		"other transport":               body(`transport stdio`),
		"duplicate can":                 body("    can \"a\"\n    can \"a\""),
		"can with children":             body(`can "a" { path "/x" }`),
		"rest node in body":             body(`base-url "https://e.invalid"`),
		"inherit in body":               body(`inherit "base.mcp.kdl"`),
		"coverage unknown kind":         body(`annotation-coverage some annotated=1 silent=1`),
		"coverage declared silent":      body(`annotation-coverage declared annotated=3 silent=1`),
		"coverage undeclared annotated": body(`annotation-coverage undeclared annotated=1 silent=0`),
		"coverage partial zero":         body(`annotation-coverage partial annotated=0 silent=2`),
		"coverage missing count":        body(`annotation-coverage declared annotated=3`),
		"coverage not a number":         body(`annotation-coverage declared annotated="3" silent=0`),
		"auth other scheme":             body(`auth bearer { value literal "x" }`),
		"auth missing value":            body(`auth header-token { header "Authorization" }`),
		"auth brace in prefix":          body(`auth header-token { header "A"; prefix "{"; value env "T" }`),
		"withhold beside wrap":          body("") + "\nwithhold \"a\" { reason \"no\" }",
		"confirm beside wrap":           body("") + "\nconfirm \"a\" { message \"sure?\" }",
		"property on wrap":              `wrap mcp upstream "x/y" foo=1 { url "https://e.invalid/mcp" }`,
		"two names":                     `wrap mcp upstream "x/y" "z" { url "https://e.invalid/mcp" }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseUpstreamSpec("", []byte(src)); err == nil {
				t.Fatalf("accepted:\n%s", src)
			}
		})
	}
}

// New serves REST grants and must not swallow a proxy guardfile into an
// opcore parse error nobody can act on.
func TestNewPointsAProxyGuardfileAtServeUpstream(t *testing.T) {
	_, err := New("x", "x.mcp.kdl", []byte(`wrap mcp upstream "x/y" { url "https://e.invalid/mcp" }`))
	if err == nil || !strings.Contains(err.Error(), "serve-upstream") {
		t.Fatalf("New error = %v, want it to name serve-upstream", err)
	}
}

// annotatedUpstream serves three tools that cover the whole evidence axis:
// declared read-only, declared mutating, and silent.
func hintedUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/mcp", hintedUpstreamHandler(t))
	return httptest.NewServer(mux)
}

func hintedUpstreamHandler(t *testing.T) http.Handler {
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
			Description: "the " + tc.name + " tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Annotations: tc.annotations,
		}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
}

// The guardfile's own text has to reach the handshake, which is the one
// thing a flag could never state (mcp-beaver#109).
func TestUpstreamSpecServesItsInstructions(t *testing.T) {
	ts := hintedUpstream(t)
	// Closed after the proxy below (LIFO), because the proxy's session holds
	// the upstream's standalone stream open and httptest waits for it.
	defer ts.Close()
	src := strings.Replace(upstreamSpecFull, "https://tandem.ac/mcp", ts.URL+"/mcp", 1)
	src = strings.Replace(src, "can \"search_docs\"\n    can get_doc", "can \"get_thing\"", 1)
	spec, err := ParseUpstreamSpec("tandem.mcp.kdl", []byte(src))
	if err != nil {
		t.Fatalf("ParseUpstreamSpec: %v", err)
	}
	s, err := NewProxyWithOptions(context.Background(), "tandem", "tandem.mcp.kdl", spec.URL, spec.Tools, spec.Options())
	if err != nil {
		t.Fatalf("NewProxyWithOptions: %v", err)
	}
	defer func() { _ = s.Close() }()
	if got := strings.Join(s.ToolNames(), ","); got != "get_thing" {
		t.Fatalf("tools = %q", got)
	}
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()
	resp := postToServer(t, proxy.Client(), proxy.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	var result struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if !strings.Contains(result.Instructions, "Tandem docs index") {
		t.Fatalf("instructions = %q, want the guardfile's own text", result.Instructions)
	}
}
