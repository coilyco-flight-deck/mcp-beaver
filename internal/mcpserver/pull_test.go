package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRegistry answers the one endpoint pull reads, for the names it knows.
func fakeRegistry(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/servers/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v0/servers/"), "/versions/latest")
		entry := map[string]any{}
		switch name {
		case "test%2Fthings":
			entry = map[string]any{
				"server": map[string]any{
					"name":        "test/things",
					"description": "  Things, read and written.\nOne api.  ",
					"remotes":     []map[string]string{{"type": "streamable-http", "url": upstreamURL}},
				},
				"_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": true}},
			}
		case "test%2Fretired":
			entry = map[string]any{
				"server": map[string]any{"name": "test/retired", "remotes": []map[string]string{{"type": "streamable-http", "url": upstreamURL}}},
				"_meta":  map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "deprecated"}},
			}
		case "test%2Flocal":
			entry = map[string]any{
				"server": map[string]any{"name": "test/local", "packages": []map[string]string{{"registryType": "npm"}}},
				"_meta":  map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active"}},
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entry)
	})
	return httptest.NewServer(mux)
}

func TestPullReadsTheUpstreamsOwnHints(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	registry := fakeRegistry(t, upstream.URL+"/mcp")
	defer registry.Close()

	pulled, err := Pull(context.Background(), "test/things", PullOptions{Registry: registry.URL})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if pulled.URL != upstream.URL+"/mcp" || pulled.Description != "Things, read and written.\nOne api." {
		t.Fatalf("url/description = %q %q", pulled.URL, pulled.Description)
	}
	var got []string
	for _, tool := range pulled.Tools {
		switch {
		case tool.ReadOnly == nil:
			got = append(got, tool.Name+":silent")
		case *tool.ReadOnly:
			got = append(got, tool.Name+":read")
		default:
			got = append(got, tool.Name+":write")
		}
	}
	// The upstream's own order, which the SDK server makes alphabetical, and
	// the silent tool told apart from the declared write.
	if want := "delete_thing:write,get_thing:read,mystery:silent"; strings.Join(got, ",") != want {
		t.Fatalf("tools = %q, want %q", strings.Join(got, ","), want)
	}
	if c := pulled.Coverage(); c != (AnnotationCoverage{Kind: "partial", Annotated: 2, Silent: 1}) {
		t.Fatalf("coverage = %+v", c)
	}
}

// Each scope is an ordered subset of the next, so widening adds lines and
// moves none. This is the acceptance criterion Kai's reorder bug became.
func TestPullScopesAreOrderedSubsets(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	pulled, err := Pull(context.Background(), "test/things", PullOptions{Upstream: upstream.URL + "/mcp"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	names := func(scope Scope) string {
		var out []string
		for _, tool := range pulled.Select(scope) {
			out = append(out, tool.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(ScopeReadOnly); got != "get_thing" {
		t.Fatalf("read-only = %q", got)
	}
	if got := names(ScopeReadWrite); got != "delete_thing,get_thing" {
		t.Fatalf("read-write = %q", got)
	}
	if got := names(ScopeAll); got != "delete_thing,get_thing,mystery" {
		t.Fatalf("all = %q", got)
	}
}

func TestRenderedGuardfileParsesBackAndCarriesTheMarker(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	registry := fakeRegistry(t, upstream.URL+"/mcp")
	defer registry.Close()
	pulled, err := Pull(context.Background(), "test/things", PullOptions{Registry: registry.URL})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for scope, want := range map[Scope][]string{
		ScopeReadOnly:  {"get_thing"},
		ScopeReadWrite: {"delete_thing", "get_thing"},
		ScopeAll:       {"delete_thing", "get_thing", "mystery"},
	} {
		text, err := RenderUpstreamGuardfile(pulled, scope)
		if err != nil {
			t.Fatalf("render %s: %v", scope, err)
		}
		spec, err := ParseUpstreamSpec("things.mcp.kdl", []byte(text))
		if err != nil {
			t.Fatalf("render %s does not parse: %v\n%s", scope, err, text)
		}
		if got := strings.Join(spec.Tools, ","); got != strings.Join(want, ",") {
			t.Fatalf("scope %s tools = %q, want %q", scope, got, strings.Join(want, ","))
		}
		if spec.Coverage == nil || spec.Coverage.Kind != "partial" || spec.Coverage.Annotated != 2 || spec.Coverage.Silent != 1 {
			t.Fatalf("scope %s coverage = %+v", scope, spec.Coverage)
		}
		// The description is folded onto one line and reaches the handshake text.
		if spec.Instructions != "Things, read and written. One api." {
			t.Fatalf("instructions = %q", spec.Instructions)
		}
		if strings.Contains(text, "withhold") {
			t.Fatalf("scope %s emitted a withhold block:\n%s", scope, text)
		}
	}
}

func TestRenderRoundTripsAHeaderTokenAuth(t *testing.T) {
	header, err := ParseUpstreamHeader("Authorization=Bearer {env:THINGS_TOKEN}", BaseProviders())
	if err != nil {
		t.Fatalf("ParseUpstreamHeader: %v", err)
	}
	pulled := &Pulled{Name: "test/things", URL: "https://e.invalid/mcp", Headers: []UpstreamHeader{header}}
	text, err := RenderUpstreamGuardfile(pulled, ScopeReadOnly)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(text, "auth header-token {") || !strings.Contains(text, `prefix "Bearer "`) || !strings.Contains(text, `value env "THINGS_TOKEN"`) {
		t.Fatalf("auth not rendered:\n%s", text)
	}
	spec, err := ParseUpstreamSpec("", []byte(text))
	if err != nil {
		t.Fatalf("parse back: %v\n%s", err, text)
	}
	if len(spec.Headers) != 1 || spec.Headers[0].Name != "Authorization" {
		t.Fatalf("headers = %+v", spec.Headers)
	}
	// A template whose value is not last cannot be stated as header-token.
	odd, _ := ParseUpstreamHeader("X-Key={env:A}-suffix", BaseProviders())
	if _, err := RenderUpstreamGuardfile(&Pulled{Name: "x", URL: "https://e.invalid/mcp", Headers: []UpstreamHeader{odd}}, ScopeReadOnly); err == nil {
		t.Fatal("rendered a header the grammar cannot state")
	}
}

func TestPullRefusesWhatTheRegistryCannotVouchFor(t *testing.T) {
	upstream := hintedUpstream(t)
	defer upstream.Close()
	registry := fakeRegistry(t, upstream.URL+"/mcp")
	defer registry.Close()
	for name, want := range map[string]string{
		"test/retired": "deprecated",
		"test/local":   "packages-only",
		"test/absent":  "no server named",
	} {
		_, err := Pull(context.Background(), name, PullOptions{Registry: registry.URL})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Pull(%q) error = %v, want %q", name, err, want)
		}
	}
}

// One registry server answered zero tools and then 25 on the same session
// shape, so pull reads twice before believing an empty list.
func TestPullRetriesAnEmptyToolList(t *testing.T) {
	inner := hintedUpstreamHandler(t)
	lists := &atomic.Int64{}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"tools/list"`)) && lists.Add(1) == 1 {
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &req)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, req.ID)
			return
		}
		inner.ServeHTTP(w, r)
	})
	flaky := httptest.NewServer(mux)
	defer flaky.Close()

	pulled, err := Pull(context.Background(), "test/things", PullOptions{Upstream: flaky.URL + "/mcp"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(pulled.Tools) != 3 || lists.Load() != 2 {
		t.Fatalf("tools = %d after %d lists, want 3 after 2", len(pulled.Tools), lists.Load())
	}
	for _, tool := range pulled.Tools {
		if tool.Name == "get_thing" && (tool.ReadOnly == nil || !*tool.ReadOnly) {
			t.Fatalf("the retried list lost its hints: %+v", tool)
		}
	}
}

// An upstream that annotates a tool without saying readOnlyHint has declared
// nothing about reads, and the SDK's typed view cannot say so.
func TestRawHintsTellSilentFromFalse(t *testing.T) {
	sse := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[" +
		"{\"name\":\"a\",\"annotations\":{\"title\":\"A\"}}," +
		"{\"name\":\"b\",\"annotations\":{\"readOnlyHint\":false}}," +
		"{\"name\":\"c\",\"annotations\":{\"readOnlyHint\":true}}," +
		"{\"name\":\"d\"}]}}\n\n"
	capture := &rawToolsCapture{raw: []*bytes.Buffer{bytes.NewBufferString(sse)}}
	hints := capture.readOnlyHints()
	if hints["a"] != nil || hints["d"] != nil {
		t.Fatalf("a and d declared nothing: %v %v", hints["a"], hints["d"])
	}
	if hints["b"] == nil || *hints["b"] || hints["c"] == nil || !*hints["c"] {
		t.Fatalf("b/c = %v %v", hints["b"], hints["c"])
	}
	if got := len(jsonRPCMessages([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))); got != 1 {
		t.Fatalf("plain JSON body split into %d messages", got)
	}
}

func TestKDLQuoteEscapes(t *testing.T) {
	if got := kdlQuote("a \"b\"\\c\nd"); got != `"a \"b\"\\c\nd"` {
		t.Fatalf("kdlQuote = %s", got)
	}
	if got := instructionsText(strings.Repeat("word ", 200)); len(got) > maxPulledInstructions+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("instructionsText did not cut: %d chars", len(got))
	}
}
