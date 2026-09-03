package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/http/mcpverb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const upstreamSpecFull = `instructions {
    text "Search the published Tandem docs index."
}

mcp-upstream "ac.tandem/docs-mcp" {
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
	spec, err := ParseUpstreamSpec("", []byte(`mcp-upstream "x/y" {
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

func TestClassifyGuardfileTellsTheShapesApart(t *testing.T) {
	for src, want := range map[string]mcpverb.Shape{
		`mcp-upstream "x/y" { url "https://e.invalid/mcp" }`:     mcpverb.ShapeUpstream,
		`wrap ward mcp forgejo { base-url "https://e.invalid" }`: mcpverb.ShapeCommand,
		`instructions { text "hi" }
mcp-upstream "x/y" { url "https://e.invalid/mcp" }`: mcpverb.ShapeUpstream,
		// The boolean shorthand opcore accepts and strict KDL does not. A
		// guardfile that will not parse classifies as nothing, so the
		// normalization has to happen before umbra sees the bytes.
		`wrap ward mcp things {
    base-url "https://e.invalid"
    can list things { path "/things"; query "q" required=true }
}`: mcpverb.ShapeCommand,
	} {
		got, err := ClassifyGuardfile([]byte(src))
		if err != nil {
			t.Fatalf("ClassifyGuardfile(%q): %v", src, err)
		}
		if got != want {
			t.Fatalf("ClassifyGuardfile(%q) = %v, want %v", src, got, want)
		}
	}
	// Neither node is not a default, it is an error.
	if _, err := ClassifyGuardfile([]byte(`instructions { text "hi" }`)); err == nil {
		t.Fatal("a guardfile carrying neither shape must refuse rather than pick one")
	}
}

// The guardfile-wide auth grammar is umbra's now, so `bearer` and `none`
// reach the proxy where the hand-rolled parser took header-token alone.
func TestParseUpstreamSpecServesTheWiderAuthGrammar(t *testing.T) {
	spec, err := ParseUpstreamSpec("", []byte(`description "Docs."

mcp-upstream "x/y" {
    url "https://e.invalid/mcp"
    auth bearer {
        value literal "unused"
    }
}`))
	if err != nil {
		t.Fatalf("ParseUpstreamSpec: %v", err)
	}
	if len(spec.Headers) != 1 || spec.Headers[0].Name != "Authorization" {
		t.Fatalf("headers = %+v, want the bearer shorthand's Authorization", spec.Headers)
	}
	// umbra owns `description` beside the node, so it parses rather than
	// meeting the unprojected-sibling refusal.
	if spec.Description != "Docs." {
		t.Fatalf("description = %q", spec.Description)
	}
	none, err := ParseUpstreamSpec("", []byte(`mcp-upstream "x/y" { url "https://e.invalid/mcp"; auth none }`))
	if err != nil {
		t.Fatalf("`auth none` is a statement, not an omission: %v", err)
	}
	if len(none.Headers) != 0 {
		t.Fatalf("headers = %+v, want none", none.Headers)
	}
}

func TestParseUpstreamSpecFailsClosed(t *testing.T) {
	body := func(inner string) string {
		return "mcp-upstream \"x/y\" {\n    url \"https://e.invalid/mcp\"\n" + inner + "\n}"
	}
	cases := map[string]string{
		"no url":                        `mcp-upstream "x/y" { can "a" }`,
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
		"auth query-param":              body("    auth query-param {\n        param key { value literal \"x\" }\n    }"),
		"auth value chain":              body("    auth bearer {\n        value { env \"A\"; literal \"b\" }\n    }"),
		"auth missing value":            body(`auth header-token { header "Authorization" }`),
		"auth brace in prefix":          body(`auth header-token { header "A"; prefix "{"; value env "T" }`),
		"confirm beside the node":       body("") + "\nconfirm \"a\" { message \"sure?\" }",
		"withhold with no reason":       body(`can "a"`) + "\nwithhold \"b\" { alternative \"a\" }",
		"withhold shadowing a grant":    body(`can "a"`) + "\nwithhold \"a\" { reason \"no\" }",
		"withhold naming a missing alternative": body(`can "a"`) +
			"\nwithhold \"b\" { reason \"no\"; alternative \"c\" }",
		"property on the node":    `mcp-upstream "x/y" foo=1 { url "https://e.invalid/mcp" }`,
		"two names":               `mcp-upstream "x/y" "z" { url "https://e.invalid/mcp" }`,
		"both shapes in one file": `wrap ward mcp x { base-url "https://e.invalid" }` + "\n" + body(""),
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
	_, err := New("x", "x.mcp.kdl", []byte(`mcp-upstream "x/y" { url "https://e.invalid/mcp" }`))
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

// withheldUpstreamSpec grants one read-only tool the hinted upstream serves and
// withholds the mutating one beside it, which is the whole hand-authoring case
// #119 named: the allowlist says what is served, and the stub says why the
// obvious neighbour is not.
func withheldUpstreamSpec(url string) string {
	return `withhold "delete_thing" {
    reason "This surface is read-only by policy."
    alternative "get_thing"
}

mcp-upstream "test/things" {
    url "` + url + `"
    can "get_thing"
}
`
}

func TestUpstreamSpecReadsWithhold(t *testing.T) {
	spec, err := ParseUpstreamSpec("things.mcp.kdl", []byte(withheldUpstreamSpec("https://e.invalid/mcp")))
	if err != nil {
		t.Fatalf("ParseUpstreamSpec: %v", err)
	}
	if got := spec.WithheldTools(); len(got) != 1 || got[0] != "delete_thing" {
		t.Fatalf("WithheldTools = %v", got)
	}
	// Carried on the options the serving path takes, not just parsed and
	// dropped on the floor.
	if len(spec.Options().Withheld) != 1 {
		t.Fatalf("Options().Withheld = %+v", spec.Options().Withheld)
	}
}

// The stub reaches the served surface a client sees, refuses every call, and
// stays out of the allowlist that the read-only checks screen.
func TestProxyServesWithheldStub(t *testing.T) {
	ts := hintedUpstream(t)
	defer ts.Close()
	spec, err := ParseUpstreamSpec("things.mcp.kdl", []byte(withheldUpstreamSpec(ts.URL+"/mcp")))
	if err != nil {
		t.Fatalf("ParseUpstreamSpec: %v", err)
	}
	s, err := NewProxyWithOptions(context.Background(), "things", "things.mcp.kdl", spec.URL, spec.Tools, spec.Options())
	if err != nil {
		t.Fatalf("NewProxyWithOptions: %v", err)
	}
	defer func() { _ = s.Close() }()
	if got := strings.Join(s.ToolNames(), ","); got != "get_thing,delete_thing" {
		t.Fatalf("ToolNames = %q, want the grant then the stub", got)
	}
	if got := s.WithheldTools(); len(got) != 1 || got[0] != "delete_thing" {
		t.Fatalf("WithheldTools = %v", got)
	}
	// The upstream annotates delete_thing readOnlyHint:false, so a stub that
	// borrowed the upstream contract would fail `--read-only strict` here. It
	// mints its own instead, and reaches no upstream to borrow from.
	if got := s.NotReadOnly(); len(got) != 0 {
		t.Fatalf("NotReadOnly = %v, want the stub spared", got)
	}

	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()
	call := postToServer(t, proxy.Client(), proxy.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_thing","arguments":{}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, call).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	if result["isError"] != true {
		t.Fatalf("isError = %v, want the stub to refuse", result["isError"])
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["error"] != withheldErrorCode || structured["alternative"] != "get_thing" {
		t.Fatalf("structured = %v", structured)
	}
}
