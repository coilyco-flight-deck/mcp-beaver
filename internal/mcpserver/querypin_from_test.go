package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// feedSpec pins the two parameters reddit's private Atom feeds carry, taking
// both out of the ONE env var that already holds the whole feed URL.
func feedSpec(baseURL string) string {
	return `pin "get_frontpage" {
    query "feed" env "REDDIT_FRONTPAGE_FEED_URL" from="query:feed"
    query "user" env "REDDIT_FRONTPAGE_FEED_URL" from="query:user"
}

wrap ward mcp reddit {
    base-url "` + baseURL + `"
    auth none
    can get frontpage {
        path "/.rss"
        raw-response
    }
}`
}

// The shape mcp-beaver#51 was blocked on: the credential is embedded in a URL,
// and the alternative was storing a second pre-split copy of the same secret.
func TestPinExtractsCredentialsFromAFeedURL(t *testing.T) {
	t.Setenv("REDDIT_FRONTPAGE_FEED_URL", "https://www.reddit.com/.rss?feed=abc123deadbeef&user=coilysiren")

	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><feed><title>private</title></feed>`))
	}))
	defer upstream.Close()

	s, err := New("reddit", "reddit.mcp.kdl", []byte(feedSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	result := callTool(t, ts, initResp.Header.Get("Mcp-Session-Id"), "2", "get_frontpage", `{}`)

	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("call reported isError; content=%v", result["content"])
	}
	for _, want := range []string{"feed=abc123deadbeef", "user=coilysiren"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("upstream query = %q, want it to carry %q", gotQuery, want)
		}
	}
	if !strings.Contains(firstText(t, result), "private") {
		t.Errorf("the feed body did not reach the caller: %s", firstText(t, result))
	}
}

// A pinned name is absent from the tool schema, so extraction cannot hand the
// model a credential it could otherwise not reach.
func TestExtractedPinStaysOutOfTheToolSchema(t *testing.T) {
	t.Setenv("REDDIT_FRONTPAGE_FEED_URL", "https://www.reddit.com/.rss?feed=abc123&user=kai")
	s, err := New("reddit", "reddit.mcp.kdl", []byte(feedSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	listResp := postToServer(t, ts.Client(), ts.URL+"/mcp", initResp.Header.Get("Mcp-Session-Id"), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	tool := findTool(t, toolList(t, decodeRPCResponse(t, listResp)), "get_frontpage")
	var schema map[string]any
	if err := toJSON(tool["inputSchema"], &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, leaked := range []string{"feed", "user"} {
		if _, ok := props[leaked]; ok {
			t.Errorf("schema exposed pinned parameter %q: %v", leaked, props)
		}
	}
}

func TestQueryPinFromFailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		from string
		want string
	}{
		"unknown extraction": {`from="header:feed"`, "want `from="},
		"no parameter named": {`from="query:"`, "want `from="},
		"empty":              {`from=""`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			spec := strings.Replace(feedSpec("http://127.0.0.1:1"), `from="query:feed"`, tc.from, 1)
			_, err := New("reddit", "reddit.mcp.kdl", []byte(spec))
			if tc.want == "" {
				// An empty from is simply no extraction, not an error.
				if err != nil {
					t.Fatalf("New rejected an absent extraction: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("New accepted %q", tc.from)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// A resolve failure is exactly when something is most likely to be logged, so
// the error names the parameter and never the value.
func TestExtractQueryParamNeverEchoesTheValue(t *testing.T) {
	secret := "https://www.reddit.com/.rss?user=kai"
	_, err := extractQueryParam(secret, "feed")
	if err == nil {
		t.Fatalf("a missing parameter was accepted")
	}
	if strings.Contains(err.Error(), "kai") || strings.Contains(err.Error(), "reddit.com") {
		t.Fatalf("error echoed the resolved value: %q", err)
	}

	if _, err := extractQueryParam("https://example.com/.rss?feed=", "feed"); err == nil {
		t.Errorf("an empty parameter was accepted")
	}
	got, err := extractQueryParam("  https://example.com/.rss?feed=tok&user=kai  ", "feed")
	if err != nil || got != "tok" {
		t.Errorf("extract = %q, %v; want tok", got, err)
	}
}
