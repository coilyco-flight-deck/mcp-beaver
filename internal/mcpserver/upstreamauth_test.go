package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/pkg/valuesource"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// providersForTest reads the same registry the runtime reads, so a test cannot
// pass against a resolver the deployment does not have.
func providersForTest() map[string]valuesource.Provider { return valuesource.Builtins() }

func TestParseUpstreamHeaderAcceptsTheTemplateForms(t *testing.T) {
	t.Setenv("TOKEN", "secret-value")
	cases := map[string]struct {
		raw  string
		name string
		want string
	}{
		"bearer prefix":     {"Authorization=Bearer {env:TOKEN}", "Authorization", "Bearer secret-value"},
		"forgejo prefix":    {"Authorization=token {env:TOKEN}", "Authorization", "token secret-value"},
		"bare value":        {"X-Api-Key={env:TOKEN}", "X-Api-Key", "secret-value"},
		"canonicalized":     {"x-api-key={env:TOKEN}", "X-Api-Key", "secret-value"},
		"prefix and suffix": {"X-Scope=ws-{env:TOKEN}-v1", "X-Scope", "ws-secret-value-v1"},
		"two sources":       {"X-Pair={env:TOKEN}/{env:TOKEN}", "X-Pair", "secret-value/secret-value"},
		"declared literal":  {"X-Workspace={literal:coilyco}", "X-Workspace", "coilyco"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			header, err := ParseUpstreamHeader(tc.raw)
			if err != nil {
				t.Fatalf("ParseUpstreamHeader(%q): %v", tc.raw, err)
			}
			if header.Name != tc.name {
				t.Errorf("name = %q, want %q", header.Name, tc.name)
			}
			got, err := header.resolve(context.Background(), providersForTest())
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseUpstreamHeaderRejects(t *testing.T) {
	cases := map[string]struct{ raw, want string }{
		"no separator":     {"Authorization", "<name>=<value template>"},
		"empty name":       {"=Bearer {env:TOKEN}", "empty header"},
		"colon in name":    {"Authorization:=Bearer {env:TOKEN}", "colon"},
		"space in name":    {"Two Words={env:TOKEN}", "whitespace"},
		"unknown provider": {"Authorization=Bearer {vault:TOKEN}", "unknown provider"},
		"empty address":    {"Authorization=Bearer {env:}", "empty address"},
		"no colon in span": {"Authorization=Bearer {TOKEN}", "{<provider>:<address>}"},
		"unclosed":         {"Authorization=Bearer {env:TOKEN", "unclosed"},
		"stray close":      {"Authorization=Bearer env:TOKEN}", "no opening"},
		"newline":          {"Authorization=Bearer {env:TOKEN}\r\nX-Evil: 1", "newline"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := ParseUpstreamHeader(tc.raw)
			if err == nil {
				t.Fatalf("ParseUpstreamHeader(%q) was accepted", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A template with no source would put the whole header value in argv, which is
// the hazard the grammar exists to make impossible by accident.
func TestParseUpstreamHeaderRefusesALiteralValueInArgv(t *testing.T) {
	_, err := ParseUpstreamHeader("Authorization=Bearer sk-live-not-a-real-token")
	if err == nil {
		t.Fatal("a bare literal header value was accepted")
	}
	if !strings.Contains(err.Error(), "argv") {
		t.Errorf("error = %q, want it to name the argv exposure", err)
	}
	if !strings.Contains(err.Error(), "{literal:") {
		t.Errorf("error = %q, want it to name the deliberate escape hatch", err)
	}
}

// Canonicalization is what makes this catch the case-varied pair, which is the
// one an author would not spot by reading their own values file.
func TestValidateUpstreamHeadersRejectsADuplicate(t *testing.T) {
	t.Setenv("TOKEN", "v")
	first, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := ParseUpstreamHeader("authorization=token {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ValidateUpstreamHeaders([]UpstreamHeader{first, second}); err == nil {
		t.Fatal("a duplicated header was accepted")
	}
	if err := ValidateUpstreamHeaders([]UpstreamHeader{first}); err != nil {
		t.Errorf("a single header was rejected: %v", err)
	}
}

func TestPreflightUpstreamHeadersFailsOnAnUnsetSecret(t *testing.T) {
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:MCP_BEAVER_ABSENT_TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = PreflightUpstreamHeaders(context.Background(), []UpstreamHeader{header})
	if err == nil {
		t.Fatal("an unset secret passed preflight")
	}
	if !strings.Contains(err.Error(), "MCP_BEAVER_ABSENT_TOKEN") {
		t.Errorf("error = %q, want it to name the address", err)
	}

	t.Setenv("MCP_BEAVER_ABSENT_TOKEN", "now-set")
	if err := PreflightUpstreamHeaders(context.Background(), []UpstreamHeader{header}); err != nil {
		t.Errorf("a resolvable header failed preflight: %v", err)
	}
}

// The value is a credential, so no error path may carry it. The newline that
// reaches this guard is an embedded one: umbra trims a surrounding one in the
// env provider itself, so a secret read from a file no longer fails the header.
func TestUpstreamHeaderErrorsNeverCarryTheValue(t *testing.T) {
	t.Setenv("TOKEN", "sk-live-should\nnever-appear")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = header.resolve(context.Background(), providersForTest())
	if err == nil {
		t.Fatal("a header value containing a newline was accepted")
	}
	if strings.Contains(err.Error(), "sk-live-should") {
		t.Fatalf("error leaked the resolved value: %q", err)
	}
}

// A trailing newline is the ordinary shape of a secret read from a file or a
// parameter store, and umbra trims it rather than refusing the header.
func TestUpstreamHeaderToleratesASurroundingNewline(t *testing.T) {
	t.Setenv("TOKEN", "sk-live-trailing\n")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := header.resolve(context.Background(), providersForTest())
	if err != nil {
		t.Fatalf("a trailing newline was refused: %v", err)
	}
	if got != "Bearer sk-live-trailing" {
		t.Errorf("resolve = %q, want the trimmed value", got)
	}
}

// Resolution is per call, matching spec-mode auth: a rotated secret takes
// effect on the next request rather than the next restart.
func TestUpstreamHeaderResolvesPerRequest(t *testing.T) {
	t.Setenv("TOKEN", "first")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := header.resolve(context.Background(), providersForTest())
	if err != nil || got != "Bearer first" {
		t.Fatalf("resolve = %q, %v", got, err)
	}
	t.Setenv("TOKEN", "second")
	got, err = header.resolve(context.Background(), providersForTest())
	if err != nil || got != "Bearer second" {
		t.Fatalf("after rotation resolve = %q, %v, want the new value", got, err)
	}
}

// Ordering guard for #79: the header wrapper must layer OVER the bounded
// transport. boundedTransport only recognizes an *http.Transport, so wrapping
// in the other order silently drops the time-to-first-byte bound.
func TestUpstreamHeadersKeepTheResponseHeaderBound(t *testing.T) {
	t.Setenv("TOKEN", "v")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client := withUpstreamHeaders(boundedUpstreamClient(nil), []UpstreamHeader{header})
	wrapper, ok := client.Transport.(*upstreamHeaderTransport)
	if !ok {
		t.Fatalf("transport = %T, want the header wrapper outermost", client.Transport)
	}
	base, ok := wrapper.base.(*http.Transport)
	if !ok {
		t.Fatalf("wrapped transport = %T, want the bounded *http.Transport", wrapper.base)
	}
	if base.ResponseHeaderTimeout != upstreamResponseHeaderTimeout {
		t.Errorf("ResponseHeaderTimeout = %v, want %v", base.ResponseHeaderTimeout, upstreamResponseHeaderTimeout)
	}
}

// A client that declared its own bound is left alone, and the wrapper must not
// mutate the caller's client while adding the header.
func TestUpstreamHeadersDoNotMutateTheCallerClient(t *testing.T) {
	t.Setenv("TOKEN", "v")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	caller := &http.Client{}
	wrapped := withUpstreamHeaders(caller, []UpstreamHeader{header})
	if caller.Transport != nil {
		t.Error("the caller's client was mutated")
	}
	if wrapped == caller {
		t.Error("the wrapper returned the caller's own client")
	}
	if withUpstreamHeaders(caller, nil) != caller {
		t.Error("no headers should leave the client untouched")
	}
}

// authedUpstream records every Authorization it sees and refuses any request
// that does not carry the expected one, which is how a hosted third-party MCP
// behaves and what serve-upstream could not satisfy before.
func authedUpstream(t *testing.T, want string) (*httptest.Server, func() []string) {
	t.Helper()
	server := upstreamTool(t, "browse", "browse upstream", `{
		"type":"object",
		"properties":{"q":{"type":"string"}}
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		q, _ := args["q"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "upstream:" + q}}}, nil, nil
	})
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	var (
		mu   sync.Mutex
		seen []string
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, got)
		mu.Unlock()
		if got != want {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// The end-to-end case deploy#647 needs: a hosted upstream that demands a
// credential, reached through the proxy with no bridge image in between.
func TestProxyPresentsTheUpstreamHeader(t *testing.T) {
	t.Setenv("MOXN_TOKEN", "workspace-token")
	upstream, seen := authedUpstream(t, "Bearer workspace-token")
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:MOXN_TOKEN}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s, err := NewProxyWithOptions(context.Background(), "proxy", "", upstream.URL+"/mcp",
		[]string{"browse"}, ProxyOptions{Headers: []UpstreamHeader{header}, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatalf("NewProxyWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json", `{"q":"ramen"}`)
	body := decodeAPIResult(t, resp)
	content, _ := body["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("body = %v, want the authenticated call to succeed", body)
	}
	first, _ := content[0].(map[string]any)
	if first["text"] != "upstream:ramen" {
		t.Errorf("upstream returned %v, want the call to have gone through", first["text"])
	}
	// The dial, the drift check, and the call each carry it. A header applied
	// only at connect time would leave every later request unauthenticated.
	requests := seen()
	if len(requests) < 2 {
		t.Fatalf("upstream saw %d requests, want the dial and the call", len(requests))
	}
	for i, got := range requests {
		if got != "Bearer workspace-token" {
			t.Errorf("request %d carried %q, want every request authenticated", i, got)
		}
	}
}

// Without the header the same upstream refuses, which is what the fleet had
// before this and proves the fixture is actually gating.
func TestProxyWithoutTheHeaderIsRefused(t *testing.T) {
	upstream, _ := authedUpstream(t, "Bearer workspace-token")
	_, err := NewProxy(context.Background(), "proxy", "", upstream.URL+"/mcp",
		[]string{"browse"}, upstream.Client())
	if err == nil {
		t.Fatal("an unauthenticated proxy connected to an authenticated upstream")
	}
}

// The admin surface names the scheme so an operator can tell an authenticated
// deployment from one that silently is not. It never carries the credential.
func TestAdminReportsTheUpstreamAuthScheme(t *testing.T) {
	if got := upstreamAuthScheme(nil); got != "none" {
		t.Errorf("no headers reported %q, want none", got)
	}
	if got := upstreamAuthScheme([]UpstreamHeader{{Name: "Authorization"}}); got != "header-token" {
		t.Errorf("headers reported %q, want header-token", got)
	}
}
