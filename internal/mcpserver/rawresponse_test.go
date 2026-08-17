package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const atomFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>reddit: the front page of the internet</title>
  <entry><title>a post</title></entry>
</feed>`

func atomSpec(baseURL, grantBody string) string {
	return `wrap ward mcp reddit {
    base-url "` + baseURL + `"
    auth bearer { value literal "unused-public-feed" }
    can get feed {
        path "/.rss"
` + grantBody + `
    }
}`
}

func serveAtom(t *testing.T, grantBody string) map[string]any {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(atomFeed))
	}))
	defer upstream.Close()

	s, err := New("reddit", "reddit.mcp.kdl", []byte(atomSpec(upstream.URL, grantBody)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	return callTool(t, ts, initResp.Header.Get("Mcp-Session-Id"), "2", "get_feed", `{}`)
}

// mcp-beaver#51's blocker. `Descriptor.RawResponse` was honoured only by
// specverb, the CLI projection, so opcore decoded JSON unconditionally and an
// Atom body failed the whole call with `invalid character '<' looking for
// beginning of value`. All four reddit reads return RSS or Atom, so none of
// them worked. umbra#289 part two added the inline node; this pins that the
// pinned umbra actually honours it on the path this runtime uses.
func TestRawResponseCarriesANonJSONBodyThrough(t *testing.T) {
	result := serveAtom(t, `        raw-response`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("raw-response call reported isError; content=%v", result["content"])
	}
	text := firstText(t, result)
	if !strings.Contains(text, "front page of the internet") {
		t.Fatalf("Atom body did not reach the caller: %s", text)
	}
	if !strings.Contains(text, `"coverage"`) {
		t.Errorf("raw body skipped the coverage envelope: %s", text)
	}
}

// The control, and the reason the node has to be stated rather than inferred:
// without it the same body is still a JSON decode failure. An author who omits
// it gets a clean error rather than a silently empty read.
func TestNonJSONBodyWithoutRawResponseStillFails(t *testing.T) {
	result := serveAtom(t, "")
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("an Atom body decoded as JSON without complaint: %v", result)
	}
}
